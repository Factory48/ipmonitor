//go:build linux && (amd64 || arm64)

package platform

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Rect is a rectangle in MyGo screen coordinates (DIPs).
type Rect struct{ X, Y, Width, Height int }

// X11 constants used by the read-only panel query.
const (
	xaAtom      = 4 // XA_ATOM
	xaCardinal  = 6 // XA_CARDINAL
	xIsViewable = 2 // MapState of a mapped, visible window

	panelMaxLongs  = 1 << 14 // cap on the property items read
	panelMinThick  = 8       // physical pixels; thinner windows are auto-hide stubs
	panelTolerance = 16      // DIPs a panel may sit away from the display edge
)

// xWindowAttributes is XWindowAttributes on LP64 Linux (amd64 and arm64).
type xWindowAttributes struct {
	X, Y, Width, Height, BorderWidth, Depth int32
	Visual                                  uintptr
	Root                                    uintptr
	Class, BitGravity, WinGravity           int32
	BackingStore                            int32
	BackingPlanes, BackingPixel             uint64
	SaveUnder                               int32
	Colormap                                uintptr
	MapInstalled, MapState                  int32
	AllEventMasks, YourEventMask            int64
	DoNotPropagateMask                      int64
	OverrideRedirect                        int32
	Screen                                  uintptr
}

var (
	// panelMu serializes queries: the Xlib error handler is process-wide and
	// is replaced only while one query runs.
	panelMu   sync.Mutex
	panelOnce sync.Once
	panelErr  error

	// panelXErrors counts X protocol errors reported to the temporary handler.
	panelXErrors atomic.Uint64
	// panelErrorCB has process lifetime: purego callbacks are never freed.
	panelErrorCB uintptr

	xOpenDisplay        func(name *byte) uintptr
	xCloseDisplay       func(dpy uintptr) int32
	xDefaultRootWindow  func(dpy uintptr) uintptr
	xInternAtom         func(dpy uintptr, name *byte, onlyIfExists int32) uintptr
	xSync               func(dpy uintptr, discard int32) int32
	xFree               func(data uintptr) int32
	xSetErrorHandler    func(handler uintptr) uintptr
	xQueryTree          func(dpy, w uintptr, root, parent, children *uintptr, n *uint32) int32
	xGetWindowAttribs   func(dpy, w uintptr, attrs *xWindowAttributes) int32
	xTranslateCoords    func(dpy, src, dest uintptr, sx, sy int32, dx, dy *int32, child *uintptr) int32
	xGetWindowProperty  func(dpy, w, prop uintptr, offset, length int, del int32, reqType uintptr, actualType *uintptr, actualFormat *int32, nitems, bytesAfter *uint64, data *uintptr) int32
	panelGdkOK          bool
	panelGdkDefaultDisp func() uintptr
	panelGdkPrimaryMon  func(display uintptr) uintptr
	panelGdkMonitor     func(display uintptr, n int32) uintptr
	panelGdkScale       func(monitor uintptr) int32
	panelGdkX11Type     func() uintptr
	panelIsInstance     func(instance, typ uintptr) bool
)

func panelBind() error {
	open := func(name string) (uintptr, error) {
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return 0, fmt.Errorf("platform: cannot load %s: %w", name, err)
		}
		return h, nil
	}
	bind := func(lib uintptr, fn any, name string) bool {
		sym, err := purego.Dlsym(lib, name)
		if err != nil || sym == 0 {
			return false
		}
		purego.RegisterFunc(fn, sym)
		return true
	}

	x11, err := open("libX11.so.6")
	if err != nil {
		return err
	}
	for _, b := range []struct {
		fn   any
		name string
	}{
		{&xOpenDisplay, "XOpenDisplay"},
		{&xCloseDisplay, "XCloseDisplay"},
		{&xDefaultRootWindow, "XDefaultRootWindow"},
		{&xInternAtom, "XInternAtom"},
		{&xSync, "XSync"},
		{&xFree, "XFree"},
		{&xSetErrorHandler, "XSetErrorHandler"},
		{&xQueryTree, "XQueryTree"},
		{&xGetWindowAttribs, "XGetWindowAttributes"},
		{&xTranslateCoords, "XTranslateCoordinates"},
		{&xGetWindowProperty, "XGetWindowProperty"},
	} {
		if !bind(x11, b.fn, b.name) {
			return fmt.Errorf("platform: missing Xlib symbol %s", b.name)
		}
	}

	// GDK only supplies the display scale; without it GDK_SCALE decides.
	gobject, err1 := open("libgobject-2.0.so.0")
	gdk, err2 := open("libgdk-3.so.0")
	if err1 == nil && err2 == nil {
		panelGdkOK = bind(gobject, &panelIsInstance, "g_type_check_instance_is_a") &&
			bind(gdk, &panelGdkDefaultDisp, "gdk_display_get_default") &&
			bind(gdk, &panelGdkPrimaryMon, "gdk_display_get_primary_monitor") &&
			bind(gdk, &panelGdkMonitor, "gdk_display_get_monitor") &&
			bind(gdk, &panelGdkScale, "gdk_monitor_get_scale_factor") &&
			bind(gdk, &panelGdkX11Type, "gdk_x11_display_get_type")
	}

	// The handler only counts errors, so a vanished window cannot make Xlib
	// exit the process.
	panelErrorCB = purego.NewCallback(func(dpy, event uintptr) uintptr {
		panelXErrors.Add(1)
		return 0
	})
	return nil
}

// panelScale returns the integer scale between MyGo DIPs and X11 pixels and
// whether GDK is on an X11 display, whose coordinates the panel shares.
func panelScale() (int, bool) {
	scale := 0
	if panelGdkOK {
		if display := panelGdkDefaultDisp(); display != 0 {
			if !panelIsInstance(display, panelGdkX11Type()) {
				return 0, false
			}
			m := panelGdkPrimaryMon(display)
			if m == 0 {
				m = panelGdkMonitor(display, 0)
			}
			if m != 0 {
				scale = int(panelGdkScale(m))
			}
		}
	}
	if scale == 0 {
		first, _, _ := strings.Cut(os.Getenv("GDK_BACKEND"), ",")
		if strings.TrimSpace(first) == "wayland" {
			return 0, false
		}
		if v, err := strconv.Atoi(os.Getenv("GDK_SCALE")); err == nil {
			scale = v
		}
	}
	return min(max(scale, 1), 8), true
}

func cstr(s string) *byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return &b[0]
}

// cptr turns an address owned by C into an unsafe.Pointer.
func cptr(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

type panelConn struct {
	dpy  uintptr
	root uintptr

	clientList, clientListStacking uintptr
	windowType, typeDock           uintptr
	strutPartial, strut            uintptr
}

// failed reports whether an X error arrived since before.
func failed(before uint64) bool { return panelXErrors.Load() != before }

// longs reads a 32-bit property as its items. It returns nil when the property
// is absent, has another type, or the window is gone.
func (c *panelConn) longs(w, prop, typ uintptr) []uint64 {
	if prop == 0 {
		return nil
	}
	before := panelXErrors.Load()
	var (
		actual     uintptr
		format     int32
		n, rest    uint64
		data       uintptr
		ok, hasVal bool
	)
	status := xGetWindowProperty(c.dpy, w, prop, 0, panelMaxLongs, 0, typ, &actual, &format, &n, &rest, &data)
	ok = status == 0 && !failed(before)
	hasVal = ok && data != 0 && actual == typ && format == 32 && n > 0
	var out []uint64
	if hasVal {
		// Format 32 items are C longs: 8 bytes on amd64 and arm64.
		n = min(n, panelMaxLongs)
		out = append(out, unsafe.Slice((*uint64)(cptr(data)), int(n))...)
	}
	if data != 0 {
		xFree(data)
	}
	return out
}

// windows collects candidate top-level windows once each: the stacking list,
// the client list, then the children of the root window (for panels that are
// not managed clients).
func (c *panelConn) windows() []uintptr {
	seen := map[uintptr]struct{}{}
	var out []uintptr
	add := func(w uintptr) {
		if w == 0 {
			return
		}
		if _, dup := seen[w]; !dup {
			seen[w] = struct{}{}
			out = append(out, w)
		}
	}
	for _, prop := range []uintptr{c.clientListStacking, c.clientList} {
		for _, w := range c.longs(c.root, prop, xaWindow) {
			add(uintptr(w))
		}
	}
	before := panelXErrors.Load()
	var root, parent, children uintptr
	var n uint32
	if xQueryTree(c.dpy, c.root, &root, &parent, &children, &n) != 0 && !failed(before) && children != 0 {
		for _, w := range unsafe.Slice((*uintptr)(cptr(children)), int(n)) {
			add(w)
		}
	}
	if children != 0 {
		xFree(children)
	}
	return out
}

const xaWindow = 33 // XA_WINDOW

type panelCandidate struct {
	x0, y0, x1, y1 int // physical pixels, clipped to the display
	horizontal     bool
	top            bool // top or left edge
	strut          bool // reserves the edge, as a real panel does
	cover          int  // per mille of the display edge it spans
}

// better ranks a panel along the top or bottom over a side dock, one that
// reserves its edge over an auto-hide one, the top over the bottom, and then
// the one spanning more of the edge.
func (a panelCandidate) better(b panelCandidate) bool {
	if a.horizontal != b.horizontal {
		return a.horizontal
	}
	if a.strut != b.strut {
		return a.strut
	}
	if a.top != b.top {
		return a.top
	}
	return a.cover > b.cover
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func ceilDiv(a, b int) int { return -floorDiv(-a, b) }

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// candidate returns the dock window w as a panel on the display d (physical
// pixels), if it is a mapped dock attached to one of its edges.
func (c *panelConn) candidate(w uintptr, d [4]int, scale int) (panelCandidate, bool) {
	isDock := false
	for _, t := range c.longs(w, c.windowType, xaAtom) {
		if uintptr(t) == c.typeDock {
			isDock = true
			break
		}
	}
	if !isDock {
		return panelCandidate{}, false
	}

	before := panelXErrors.Load()
	var attrs xWindowAttributes
	if xGetWindowAttribs(c.dpy, w, &attrs) == 0 || failed(before) || attrs.MapState != xIsViewable {
		return panelCandidate{}, false
	}
	var rx, ry int32
	var child uintptr
	if xTranslateCoords(c.dpy, w, c.root, 0, 0, &rx, &ry, &child) == 0 || failed(before) {
		return panelCandidate{}, false
	}
	bw := int(attrs.BorderWidth)
	x0, y0 := int(rx)-bw, int(ry)-bw
	x1, y1 := x0+int(attrs.Width)+2*bw, y0+int(attrs.Height)+2*bw

	// Clip to the display: a panel may span several monitors.
	dx0, dy0, dx1, dy1 := d[0], d[1], d[2], d[3]
	cx0, cy0, cx1, cy1 := max(x0, dx0), max(y0, dy0), min(x1, dx1), min(y1, dy1)
	cw, ch := cx1-cx0, cy1-cy0
	dw, dh := dx1-dx0, dy1-dy0
	if cw <= 0 || ch <= 0 {
		return panelCandidate{}, false
	}

	tol := panelTolerance * scale
	cand := panelCandidate{x0: cx0, y0: cy0, x1: cx1, y1: cy1}
	var edge int // index into the strut: left, right, top, bottom
	switch {
	case ch >= panelMinThick && ch*2 <= dh && cw*2 >= dw:
		cand.horizontal = true
		cand.cover = cw * 1000 / dw
		dTop, dBottom := abs(y0-dy0), abs(y1-dy1)
		switch {
		case dTop <= tol && dTop <= dBottom:
			cand.top, edge = true, 2
		case dBottom <= tol:
			edge = 3
		default:
			return panelCandidate{}, false
		}
	case cw >= panelMinThick && cw*2 <= dw && ch*2 >= dh:
		cand.cover = ch * 1000 / dh
		dLeft, dRight := abs(x0-dx0), abs(x1-dx1)
		switch {
		case dLeft <= tol && dLeft <= dRight:
			cand.top, edge = true, 0
		case dRight <= tol:
			edge = 1
		default:
			return panelCandidate{}, false
		}
	default:
		return panelCandidate{}, false
	}

	strut := c.longs(w, c.strutPartial, xaCardinal)
	if len(strut) < 4 {
		strut = c.longs(w, c.strut, xaCardinal)
	}
	cand.strut = len(strut) >= 4 && strut[edge] > 0
	return cand, true
}

func (c *panelConn) search(d [4]int, scale int) (panelCandidate, bool) {
	var best panelCandidate
	found := false
	for _, w := range c.windows() {
		cand, ok := c.candidate(w, d, scale)
		if ok && (!found || cand.better(best)) {
			best, found = cand, true
		}
	}
	return best, found
}

// PanelBounds returns the bounds, in MyGo DIPs, of the system panel (an X11
// _NET_WM_WINDOW_TYPE_DOCK window) attached to an edge of display, clipped to
// it. It reads the X11/XWayland server named by $DISPLAY, where AppIndicator
// hosts live, over a short-lived connection; nothing is watched or changed.
// Call it on MyGo's main thread, once per popup. It reports false without a
// reachable X11 display, when GDK uses another backend, and when no panel is
// visible there (for instance a panel drawn by a Wayland compositor or by
// GNOME Shell, which does not expose it to X11 clients).
func PanelBounds(display Rect) (Rect, bool) {
	if display.Width <= 0 || display.Height <= 0 || os.Getenv("DISPLAY") == "" {
		return Rect{}, false
	}
	panelMu.Lock()
	defer panelMu.Unlock()
	panelOnce.Do(func() { panelErr = panelBind() })
	if panelErr != nil {
		return Rect{}, false
	}
	scale, ok := panelScale()
	if !ok {
		return Rect{}, false
	}

	dpy := xOpenDisplay(nil)
	if dpy == 0 {
		return Rect{}, false
	}
	previous := xSetErrorHandler(panelErrorCB)
	defer func() {
		xSync(dpy, 0) // report pending errors while the handler is ours
		xSetErrorHandler(previous)
		xCloseDisplay(dpy)
	}()

	c := &panelConn{dpy: dpy, root: xDefaultRootWindow(dpy)}
	atom := func(name string) uintptr { return xInternAtom(dpy, cstr(name), 1) }
	c.windowType = atom("_NET_WM_WINDOW_TYPE")
	c.typeDock = atom("_NET_WM_WINDOW_TYPE_DOCK")
	if c.root == 0 || c.windowType == 0 || c.typeDock == 0 {
		return Rect{}, false // no window manager has used the dock type
	}
	c.clientList = atom("_NET_CLIENT_LIST")
	c.clientListStacking = atom("_NET_CLIENT_LIST_STACKING")
	c.strutPartial = atom("_NET_WM_STRUT_PARTIAL")
	c.strut = atom("_NET_WM_STRUT")

	phys := [4]int{
		display.X * scale, display.Y * scale,
		(display.X + display.Width) * scale, (display.Y + display.Height) * scale,
	}
	best, found := c.search(phys, scale)
	if !found {
		return Rect{}, false
	}
	x, y := floorDiv(best.x0, scale), floorDiv(best.y0, scale)
	return Rect{
		X: x, Y: y,
		Width:  ceilDiv(best.x1, scale) - x,
		Height: ceilDiv(best.y1, scale) - y,
	}, true
}
