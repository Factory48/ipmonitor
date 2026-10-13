//go:build linux && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// GTK runs the watcher and its asynchronous requests on MyGo's main thread.
// Background clipboard access requires an X11/XWayland display: a native
// Wayland client receives selections only while it has keyboard focus.

type gtkWatcher struct {
	onText  func(string)
	board   uintptr
	handler uint64
	id      uintptr
	req     uintptr
	stopped bool
}

type gtkRequest struct {
	w      *gtkWatcher
	latin1 bool
}

type gtkTextTarget struct {
	atom   uintptr
	latin1 bool
}

var (
	gtkOnce sync.Once
	gtkErr  error

	gdkAtomIntern               func(name *byte, onlyIfExists int32) uintptr
	gdkDisplayGetDefault        func() uintptr
	gdkX11DisplayGetType        func() uintptr
	gTypeCheckInstanceIsA       func(instance, typ uintptr) bool
	gtkClipboardGet             func(selection uintptr) uintptr
	gtkClipboardRequestTargets  func(board, callback, data uintptr)
	gtkClipboardRequestContents func(board, target, callback, data uintptr)
	gtkSelectionDataGetLength   func(sd uintptr) int32
	gtkSelectionDataGetFormat   func(sd uintptr) int32
	gtkSelectionDataGetData     func(sd uintptr) uintptr
	gSignalConnectData          func(instance uintptr, signal *byte, handler, data, destroy uintptr, flags int32) uint64
	gSignalHandlerDisconnect    func(instance uintptr, id uint64)
	gtkOwnerCB, gtkTargetsCB    uintptr
	gtkContentsCB               uintptr
	gtkClipboardAtom            uintptr
	gtkTextTargets              []gtkTextTarget
	gtkWatchers                 = map[uintptr]*gtkWatcher{}
	gtkRequests                 = map[uintptr]gtkRequest{}
	gtkNextID                   uintptr
)

func gtkInit() error {
	gtkOnce.Do(func() { gtkErr = gtkBind() })
	return gtkErr
}

func gtkBind() error {
	open := func(name string) (uintptr, error) {
		h, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return 0, fmt.Errorf("platform: cannot load %s: %w", name, err)
		}
		return h, nil
	}
	gobject, err := open("libgobject-2.0.so.0")
	if err != nil {
		return err
	}
	gdk, err := open("libgdk-3.so.0")
	if err != nil {
		return err
	}
	gtk, err := open("libgtk-3.so.0")
	if err != nil {
		return err
	}
	binds := []struct {
		lib  uintptr
		fn   any
		name string
	}{
		{gobject, &gSignalConnectData, "g_signal_connect_data"},
		{gobject, &gSignalHandlerDisconnect, "g_signal_handler_disconnect"},
		{gobject, &gTypeCheckInstanceIsA, "g_type_check_instance_is_a"},
		{gdk, &gdkAtomIntern, "gdk_atom_intern"},
		{gdk, &gdkDisplayGetDefault, "gdk_display_get_default"},
		{gdk, &gdkX11DisplayGetType, "gdk_x11_display_get_type"},
		{gtk, &gtkClipboardGet, "gtk_clipboard_get"},
		{gtk, &gtkClipboardRequestTargets, "gtk_clipboard_request_targets"},
		{gtk, &gtkClipboardRequestContents, "gtk_clipboard_request_contents"},
		{gtk, &gtkSelectionDataGetLength, "gtk_selection_data_get_length"},
		{gtk, &gtkSelectionDataGetFormat, "gtk_selection_data_get_format"},
		{gtk, &gtkSelectionDataGetData, "gtk_selection_data_get_data"},
	}
	for _, b := range binds {
		sym, err := purego.Dlsym(b.lib, b.name)
		if err != nil || sym == 0 {
			return fmt.Errorf("platform: missing GTK symbol %s", b.name)
		}
		purego.RegisterFunc(b.fn, sym)
	}
	intern := func(name string) uintptr {
		b := make([]byte, len(name)+1)
		copy(b, name)
		return gdkAtomIntern(&b[0], 0)
	}
	gtkClipboardAtom = intern("CLIPBOARD")
	for _, target := range []struct {
		name   string
		latin1 bool
	}{
		{"UTF8_STRING", false},
		{"text/plain;charset=utf-8", false},
		{"text/plain", false},
		{"STRING", true},
	} {
		if atom := intern(target.name); atom != 0 {
			gtkTextTargets = append(gtkTextTargets, gtkTextTarget{atom, target.latin1})
		}
	}
	if gtkClipboardAtom == 0 || len(gtkTextTargets) == 0 {
		return errors.New("platform: cannot intern GDK clipboard atoms")
	}
	// purego callback trampolines have process lifetime; create each only once.
	gtkOwnerCB = purego.NewCallback(gtkOnOwnerChange)
	gtkTargetsCB = purego.NewCallback(gtkOnTargets)
	gtkContentsCB = purego.NewCallback(gtkOnContents)
	return nil
}

func gtkNewRequest(w *gtkWatcher, latin1 bool) uintptr {
	delete(gtkRequests, w.req)
	gtkNextID++
	w.req = gtkNextID
	gtkRequests[w.req] = gtkRequest{w: w, latin1: latin1}
	return w.req
}

func gtkTakeRequest(id uintptr) (gtkRequest, bool) {
	r, ok := gtkRequests[id]
	if !ok {
		return gtkRequest{}, false
	}
	delete(gtkRequests, id)
	if r.w.stopped || r.w.req != id {
		return gtkRequest{}, false
	}
	r.w.req = 0
	return r, true
}

func gtkOnOwnerChange(board, event, id uintptr) {
	w := gtkWatchers[id]
	if w == nil || w.stopped {
		return
	}
	gtkClipboardRequestTargets(board, gtkTargetsCB, gtkNewRequest(w, false))
}

func gtkOnTargets(board, atoms uintptr, n int32, id uintptr) {
	r, ok := gtkTakeRequest(id)
	if !ok || atoms == 0 || n <= 0 || n > 4096 {
		return
	}
	base := *(*unsafe.Pointer)(unsafe.Pointer(&atoms))
	best := -1
	for i := range int(n) {
		atom := *(*uintptr)(unsafe.Add(base, uintptr(i)*unsafe.Sizeof(uintptr(0))))
		for rank, target := range gtkTextTargets {
			if target.atom == atom && (best < 0 || rank < best) {
				best = rank
			}
		}
	}
	if best < 0 {
		return
	}
	target := gtkTextTargets[best]
	gtkClipboardRequestContents(r.w.board, target.atom, gtkContentsCB, gtkNewRequest(r.w, target.latin1))
}

func gtkOnContents(board, sd, id uintptr) {
	r, ok := gtkTakeRequest(id)
	if !ok || sd == 0 {
		return
	}
	limit := maxUTF8Bytes
	if r.latin1 {
		limit = maxTextUnits
	}
	n := gtkSelectionDataGetLength(sd)
	if n <= 0 || int(n) > limit || gtkSelectionDataGetFormat(sd) != 8 {
		return
	}
	p := gtkSelectionDataGetData(sd)
	if p == 0 {
		return
	}
	b := unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), int(n))
	var text string
	if r.latin1 {
		text, ok = textFromLatin1(b)
	} else {
		text, ok = textFromUTF8(b)
	}
	if ok {
		r.w.onText(text)
	}
}

// WatchClipboard watches later changes without reading the startup clipboard
// or polling. Call it and stop on MyGo's main thread after GTK initialization.
// The callback runs on that thread and must return quickly.
func WatchClipboard(onText func(string)) (stop func(), err error) {
	if onText == nil {
		return nil, errors.New("platform: nil clipboard callback")
	}
	if err := gtkInit(); err != nil {
		return nil, err
	}
	display := gdkDisplayGetDefault()
	if display == 0 {
		return nil, errors.New("platform: GTK has no default display")
	}
	if !gTypeCheckInstanceIsA(display, gdkX11DisplayGetType()) {
		return nil, fmt.Errorf("%w: background clipboard monitoring requires X11/XWayland", ErrUnsupported)
	}
	board := gtkClipboardGet(gtkClipboardAtom)
	if board == 0 {
		return nil, errors.New("platform: cannot get the GTK clipboard")
	}
	gtkNextID++
	w := &gtkWatcher{onText: onText, board: board, id: gtkNextID}
	gtkWatchers[w.id] = w
	signal := []byte("owner-change\x00")
	w.handler = gSignalConnectData(board, &signal[0], gtkOwnerCB, w.id, 0, 0)
	if w.handler == 0 {
		delete(gtkWatchers, w.id)
		return nil, errors.New("platform: cannot watch the GTK clipboard")
	}
	return func() {
		if w.stopped {
			return
		}
		w.stopped = true
		gSignalHandlerDisconnect(w.board, w.handler)
		delete(gtkWatchers, w.id)
		for id, request := range gtkRequests {
			if request.w == w {
				delete(gtkRequests, id)
			}
		}
		w.onText = nil
	}, nil
}
