package main

import (
	"bytes"
	"container/list"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/egoist/mygo/ui"
	tsfont "github.com/go-text/typesetting/font"
	otf "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/vector"
)

// Country flags are a port of the original C++ icons.cpp (Flag, Build and the
// per-flag helpers). Each flag is drawn on a 4x canvas with the original
// formulas, box-filtered down to flagBitmapSize pixels and centered as a 3:2
// rectangle on a transparent square, exactly like the original Build().
// HK petals and the CA maple leaf use geometry adapted from flag-icons (MIT);
// see assets/flag-icons-LICENSE.txt. The fallback label uses Go Bold, see
// assets/Go-fonts-LICENSE.txt.

const (
	flagBitmapSize    = 96 // square bitmap, in pixels
	flagSupersample   = 4
	countryCacheLimit = 128
	flagPi            = math.Pi
)

var (
	flagBlue  = flagRGB(0, 70, 150)
	flagWhite = flagRGB(255, 255, 255)
	flagBlack = flagRGB(0, 0, 0)
)

func flagRGB(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

type flagPoint struct{ x, y float64 }

// ---- cache --------------------------------------------------------------

type countryEntry struct {
	code   string
	bitmap *ui.Bitmap
}

var countryCache struct {
	sync.Mutex
	order *list.List // front = most recently used
	items map[string]*list.Element
}

// normalizeCountryCode returns an upper-case ASCII ISO-like two letter code,
// or "??" for anything else.
func normalizeCountryCode(code string) string {
	if len(code) != 2 {
		return "??"
	}
	var b [2]byte
	for i := range 2 {
		ch := code[i]
		if ch >= 'a' && ch <= 'z' {
			ch -= 'a' - 'A'
		}
		if ch < 'A' || ch > 'Z' {
			return "??"
		}
		b[i] = ch
	}
	return string(b[:])
}

// countryImage returns the cached bitmap for code (never nil). The bitmap is a
// square holding the 3:2 flag centered on transparent pixels; show it with a
// square size. Unknown codes get a blue badge with the code in white, "??" for
// invalid input.
func countryImage(code string) ui.ImageSource {
	code = normalizeCountryCode(code)
	cache := &countryCache
	cache.Lock()
	defer cache.Unlock()
	if cache.items == nil {
		cache.order = list.New()
		cache.items = make(map[string]*list.Element, countryCacheLimit)
	}
	if el, ok := cache.items[code]; ok {
		cache.order.MoveToFront(el)
		return el.Value.(*countryEntry).bitmap
	}
	for cache.order.Len() >= countryCacheLimit {
		oldest := cache.order.Back()
		delete(cache.items, oldest.Value.(*countryEntry).code)
		cache.order.Remove(oldest)
	}
	entry := &countryEntry{code: code, bitmap: ui.NewBitmap(renderCountryImage(code))}
	cache.items[code] = cache.order.PushFront(entry)
	return entry.bitmap
}

// countryBadge shows the flag of code in a size x size square, the 3:2 flag
// centered inside it.
func countryBadge(c *ui.Context, code string, size float32) ui.Element {
	return ui.Image(c, countryImage(code)).Size(size, size)
}

// ---- Build --------------------------------------------------------------

func renderCountryImage(code string) *image.RGBA {
	const size = flagBitmapSize
	height := (size*2 + 1) / 3
	top := (size - height) / 2
	src := newFlagCanvas(size*flagSupersample, height*flagSupersample)
	drawFlag(src, code)

	out := image.NewRGBA(image.Rect(0, 0, size, size))
	const n = flagSupersample * flagSupersample
	for y := range height {
		for x := range size {
			var r, g, b uint32
			for dy := range flagSupersample {
				i := src.img.PixOffset(x*flagSupersample, y*flagSupersample+dy)
				for dx := range flagSupersample {
					p := src.img.Pix[i+dx*4 : i+dx*4+3 : i+dx*4+3]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
				}
			}
			o := out.PixOffset(x, top+y)
			out.Pix[o+0] = uint8((r + n/2) / n)
			out.Pix[o+1] = uint8((g + n/2) / n)
			out.Pix[o+2] = uint8((b + n/2) / n)
			out.Pix[o+3] = 255
		}
	}
	return out
}

// ---- canvas -------------------------------------------------------------

const (
	segMove uint8 = iota
	segLine
	segQuad
	segCube
	segClose
)

type flagSeg struct {
	op uint8
	p  [6]float32
}

type flagCanvas struct {
	img        *image.RGBA
	w, h       int
	ras        vector.Rasterizer
	segs       []flagSeg
	minX, minY float64
	maxX, maxY float64
}

func newFlagCanvas(w, h int) *flagCanvas {
	c := &flagCanvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), w: w, h: h}
	c.resetPath()
	return c
}

func (c *flagCanvas) resetPath() {
	c.segs = c.segs[:0]
	c.minX, c.minY = math.Inf(1), math.Inf(1)
	c.maxX, c.maxY = math.Inf(-1), math.Inf(-1)
}

func (c *flagCanvas) bound(x, y float64) {
	c.minX, c.maxX = math.Min(c.minX, x), math.Max(c.maxX, x)
	c.minY, c.maxY = math.Min(c.minY, y), math.Max(c.maxY, y)
}

func (c *flagCanvas) add(op uint8, pts ...float64) {
	s := flagSeg{op: op}
	for i := 0; i+1 < len(pts); i += 2 {
		s.p[i], s.p[i+1] = float32(pts[i]), float32(pts[i+1])
		c.bound(pts[i], pts[i+1])
	}
	c.segs = append(c.segs, s)
}

func (c *flagCanvas) move(x, y float64) { c.add(segMove, x, y) }
func (c *flagCanvas) line(x, y float64) { c.add(segLine, x, y) }
func (c *flagCanvas) quad(x1, y1, x2, y2 float64) {
	c.add(segQuad, x1, y1, x2, y2)
}
func (c *flagCanvas) cube(x1, y1, x2, y2, x3, y3 float64) {
	c.add(segCube, x1, y1, x2, y2, x3, y3)
}
func (c *flagCanvas) closePath() { c.segs = append(c.segs, flagSeg{op: segClose}) }

// fill rasterizes the current path (anti-aliased, non-zero style accumulation)
// with an opaque color and clears it.
func (c *flagCanvas) fill(col color.RGBA) {
	defer c.resetPath()
	if len(c.segs) == 0 {
		return
	}
	r := image.Rect(int(math.Floor(c.minX)), int(math.Floor(c.minY)),
		int(math.Ceil(c.maxX)), int(math.Ceil(c.maxY))).Intersect(c.img.Rect)
	if r.Empty() {
		return
	}
	z := &c.ras
	z.Reset(r.Dx(), r.Dy())
	ox, oy := float32(r.Min.X), float32(r.Min.Y)
	open := false
	for i := range c.segs {
		s := &c.segs[i]
		switch s.op {
		case segMove:
			if open {
				z.ClosePath()
			}
			z.MoveTo(s.p[0]-ox, s.p[1]-oy)
			open = true
		case segLine:
			z.LineTo(s.p[0]-ox, s.p[1]-oy)
		case segQuad:
			z.QuadTo(s.p[0]-ox, s.p[1]-oy, s.p[2]-ox, s.p[3]-oy)
		case segCube:
			z.CubeTo(s.p[0]-ox, s.p[1]-oy, s.p[2]-ox, s.p[3]-oy, s.p[4]-ox, s.p[5]-oy)
		case segClose:
			z.ClosePath()
		}
	}
	if open {
		z.ClosePath()
	}
	z.Draw(c.img, r, image.NewUniform(col), image.Point{})
}

// rect is the original Rect(): an integer, pixel-exact FillRect.
func (c *flagCanvas) rect(x, y, w, h int, col color.RGBA) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.img.Rect)
	if r.Empty() {
		return
	}
	first := c.img.PixOffset(r.Min.X, r.Min.Y)
	row := c.img.Pix[first : first+r.Dx()*4]
	for i := 0; i < len(row); i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = col.R, col.G, col.B, 255
	}
	for yy := r.Min.Y + 1; yy < r.Max.Y; yy++ {
		o := c.img.PixOffset(r.Min.X, yy)
		copy(c.img.Pix[o:o+len(row)], row)
	}
}

// poly is the original Shape(): a filled polygon.
func (c *flagCanvas) poly(pts []flagPoint, col color.RGBA) {
	for i, p := range pts {
		if i == 0 {
			c.move(p.x, p.y)
		} else {
			c.line(p.x, p.y)
		}
	}
	c.closePath()
	c.fill(col)
}

// oval is the original Oval(): the ellipse inscribed in [x,x+w) x [y,y+h).
func (c *flagCanvas) oval(x, y, w, h float64, col color.RGBA) {
	const k = 0.5522847498307936
	rx, ry := w/2, h/2
	cx, cy := x+rx, y+ry
	c.move(cx+rx, cy)
	c.cube(cx+rx, cy+k*ry, cx+k*rx, cy+ry, cx, cy+ry)
	c.cube(cx-k*rx, cy+ry, cx-rx, cy+k*ry, cx-rx, cy)
	c.cube(cx-rx, cy-k*ry, cx-k*rx, cy-ry, cx, cy-ry)
	c.cube(cx+k*rx, cy-ry, cx+rx, cy-k*ry, cx+rx, cy)
	c.closePath()
	c.fill(col)
}

// star is the original Star(): five-point star, inner radius 0.38196601125.
func (c *flagCanvas) star(x, y, radius float64, col color.RGBA, angle float64) {
	var p [10]flagPoint
	for i := range p {
		a := angle + float64(i)*flagPi/5
		r := radius
		if i%2 != 0 {
			r *= 0.38196601125
		}
		p[i] = flagPoint{x + r*math.Cos(a), y + r*math.Sin(a)}
	}
	c.poly(p[:], col)
}

const flagStarUp = -flagPi / 2

// ---- label --------------------------------------------------------------

var flagFace = sync.OnceValue(func() *tsfont.Face {
	face, err := tsfont.ParseTTF(bytes.NewReader(gobold.TTF))
	if err != nil {
		return nil
	}
	return face
})

// label is the original Label(): blue field, bold white text, centered, shrunk
// until it fits in 90% of the width.
func (c *flagCanvas) label(text string) {
	c.rect(0, 0, c.w, c.h, flagBlue)
	face := flagFace()
	if face == nil {
		return
	}
	upem := float64(face.Upem())
	type glyph struct {
		gid     tsfont.GID
		advance float64
	}
	glyphs := make([]glyph, 0, len(text))
	total := 0.0
	for _, ch := range text {
		gid, ok := face.NominalGlyph(ch)
		if !ok {
			continue
		}
		adv := float64(face.HorizontalAdvance(gid))
		glyphs = append(glyphs, glyph{gid, adv})
		total += adv
	}
	em := float64(c.h * 4 / 5)
	limit := float64(c.w * 9 / 10)
	for total/upem*em > limit && em > 4 {
		em -= 2
	}
	scale := em / upem
	ext, ok := face.FontHExtents()
	asc, desc := float64(ext.Ascender), math.Abs(float64(ext.Descender))
	if !ok || asc+desc <= 0 {
		asc, desc = 0.8*upem, 0.2*upem
	}
	baseline := (float64(c.h)-(asc+desc)*scale)/2 + asc*scale
	pen := (float64(c.w) - total*scale) / 2
	for _, g := range glyphs {
		if out, ok := face.GlyphDataOutline(g.gid); ok {
			tx := func(p otf.SegmentPoint) (float64, float64) {
				return pen + float64(p.X)*scale, baseline - float64(p.Y)*scale
			}
			for _, s := range out.Segments {
				a := s.Args
				switch s.Op {
				case otf.SegmentOpMoveTo:
					if len(c.segs) > 0 {
						c.closePath()
					}
					x, y := tx(a[0])
					c.move(x, y)
				case otf.SegmentOpLineTo:
					x, y := tx(a[0])
					c.line(x, y)
				case otf.SegmentOpQuadTo:
					x1, y1 := tx(a[0])
					x2, y2 := tx(a[1])
					c.quad(x1, y1, x2, y2)
				case otf.SegmentOpCubeTo:
					x1, y1 := tx(a[0])
					x2, y2 := tx(a[1])
					x3, y3 := tx(a[2])
					c.cube(x1, y1, x2, y2, x3, y3)
				}
			}
			c.closePath()
		}
		pen += g.advance * scale
	}
	c.fill(flagWhite)
}

// ---- flags --------------------------------------------------------------

func (c *flagCanvas) unionJack() {
	w, h := c.w, c.h
	fw, fh := float64(w), float64(h)
	red := flagRGB(200, 16, 46)
	c.rect(0, 0, w, h, flagRGB(1, 33, 105))
	// Coordinates use a 60x40 field; the red saltire is counterchanged.
	polygon := func(v [][2]float64, col color.RGBA) {
		p := make([]flagPoint, len(v))
		for i := range v {
			p[i] = flagPoint{v[i][0] * fw / 60, v[i][1] * fh / 40}
		}
		c.poly(p, col)
	}
	polygon([][2]float64{{-6, 0}, {0, -4}, {66, 40}, {60, 44}}, flagWhite)
	polygon([][2]float64{{60, -4}, {66, 0}, {0, 44}, {-6, 40}}, flagWhite)
	polygon([][2]float64{{0, 0}, {30, 20}, {30, 22.6667}, {0, 2.6667}}, red)
	polygon([][2]float64{{30, 17.3333}, {60, 37.3333}, {60, 40}, {30, 20}}, red)
	polygon([][2]float64{{30, 20}, {60, 0}, {56, 0}, {26, 20}}, red)
	polygon([][2]float64{{0, 40}, {4, 40}, {34, 20}, {30, 20}}, red)
	c.rect(0, h*15/40, w, h*10/40, flagWhite)
	c.rect(w*25/60, 0, w*10/60, h, flagWhite)
	c.rect(0, h*17/40, w, h*6/40, red)
	c.rect(w*27/60, 0, w*6/60, h, red)
}

func (c *flagCanvas) korea() {
	h := float64(c.h)
	cx, cy, r := float64(c.w)/2, h/2, h/4
	angle := math.Atan2(2, 3)
	sinA, cosA := math.Sincos(angle)
	point := func(x, y float64) flagPoint {
		return flagPoint{cx + x*cosA - y*sinA, cy + x*sinA + y*cosA}
	}
	red, blue := flagRGB(205, 46, 58), flagRGB(0, 71, 160)
	c.oval(cx-r, cy-r, 2*r, 2*r, blue)
	half := make([]flagPoint, 65)
	for i := range half {
		a := flagPi + float64(i)*flagPi/64
		half[i] = point(r*math.Cos(a), r*math.Sin(a))
	}
	c.poly(half, red)
	for _, sign := range []float64{-1, 1} {
		x := cx + sign*r/2*cosA
		y := cy + sign*r/2*sinA
		col := blue
		if sign < 0 {
			col = red
		}
		c.oval(x-r/2, y-r/2, r, r, col)
	}
	// Geon (NW), Gon (SE), Gam (NE), Ri (SW). Bit = split bar.
	for corner := range 4 {
		sx, sy := -1.0, -1.0
		if corner%2 != 0 {
			sx = 1
		}
		if corner/2 != 0 {
			sy = 1
		}
		broken := 2
		switch corner {
		case 0:
			broken = 0
		case 3:
			broken = 7
		case 1:
			broken = 5
		}
		a := math.Atan2(sy*2, sx*3)
		uy, ux := math.Sincos(a)
		length, thick, gap := h/4, h/24, h/48
		for row := range 3 {
			radial := h*(1.0/4+1.0/12) + thick/2 + float64(row)*(thick+gap)
			split := broken&(1<<row) != 0
			parts := 1
			if split {
				parts = 2
			}
			for part := range parts {
				lo, hi := -length/2, length/2
				if split {
					if part == 0 {
						hi = -gap / 2
					} else {
						lo = gap / 2
					}
				}
				along := [4]float64{lo, hi, hi, lo}
				across := [4]float64{-thick / 2, -thick / 2, thick / 2, thick / 2}
				var p [4]flagPoint
				for j := range p {
					p[j] = flagPoint{
						cx + (radial+across[j])*ux - along[j]*uy,
						cy + (radial+across[j])*uy + along[j]*ux,
					}
				}
				c.poly(p[:], flagBlack)
			}
		}
	}
}

func (c *flagCanvas) hongKong() {
	red := flagRGB(236, 27, 46)
	c.rect(0, 0, c.w, c.h, red)
	scale := float64(c.h) / 480.0
	for petal := range 5 {
		sinA, cosA := math.Sincos(float64(petal) * 2 * flagPi / 5)
		pt := func(x, y float64) (float64, float64) {
			x -= 312.5
			y -= 243.5
			return float64(c.w)/2.0 + scale*(x*cosA-y*sinA), float64(c.h)/2.0 + scale*(x*sinA+y*cosA)
		}
		move := func(x, y float64) { px, py := pt(x, y); c.move(px, py) }
		line := func(x, y float64) { px, py := pt(x, y); c.line(px, py) }
		cube := func(x1, y1, x2, y2, x3, y3 float64) {
			ax, ay := pt(x1, y1)
			bx, by := pt(x2, y2)
			cx, cy := pt(x3, y3)
			c.cube(ax, ay, bx, by, cx, cy)
		}
		move(346.3, 103.1)
		cube(267, 98, 230.6, 201.9, 305.6, 240.3)
		cube(279.6, 217.9, 285, 185, 295.5, 167.9)
		line(297.4, 169)
		cube(283.6, 192.5, 286.2, 221.7, 308.5, 240)
		cube(295.8, 227.7, 299, 201, 320.6, 191.1)
		cube(342.2, 181.2, 344.2, 151.8, 337, 142)
		cube(327.2, 124.933333, 330.3, 111.966667, 346.3, 103.1)
		c.closePath()
		c.fill(flagWhite)
		star := [10][2]float64{{307.9, 164}, {303.2, 171.4}, {301.4, 162.8}, {292.8, 160.5},
			{300.6, 156.2}, {300, 147.3}, {306.5, 153.4}, {314.8, 150.1}, {311.1, 158.2}, {316.7, 165}}
		var p [10]flagPoint
		for i := range p {
			x, y := pt(star[i][0], star[i][1])
			p[i] = flagPoint{x, y}
		}
		c.poly(p[:], red)
	}
}

func drawFlag(c *flagCanvas, code string) {
	w, h := c.w, c.h
	fw, fh := float64(w), float64(h)
	c.rect(0, 0, w, h, flagWhite)
	switch code {
	case "CN":
		c.rect(0, 0, w, h, flagRGB(222, 41, 16))
		gold := flagRGB(255, 222, 0)
		x, y := fw/6.0, fh/4.0
		c.star(x, y, fh*0.15, gold, flagStarUp)
		for _, p := range [4][2]float64{{10, 2}, {12, 4}, {12, 7}, {10, 9}} {
			sx, sy := p[0]*fw/30, p[1]*fh/20
			c.star(sx, sy, fh*0.05, gold, math.Atan2(y-sy, x-sx))
		}
	case "US":
		for i := 0; i < 13; i += 2 {
			c.rect(0, i*h/13, w, (i+1)*h/13-i*h/13, flagRGB(178, 34, 52))
		}
		cw, ch := w*2/5, h*7/13
		c.rect(0, 0, cw, ch, flagRGB(60, 59, 110))
		for row := range 9 {
			cols, off := 6, 1
			if row%2 != 0 {
				cols, off = 5, 2
			}
			for col := range cols {
				c.star(float64(col*2+off)*float64(cw)/12.0, float64(row+1)*float64(ch)/10.0,
					fh*0.0308, flagWhite, flagStarUp)
			}
		}
	case "KR":
		c.korea()
	case "HK":
		c.hongKong()
	case "RU":
		c.rect(0, h/3, w, h*2/3-h/3, flagRGB(0, 57, 166))
		c.rect(0, h*2/3, w, h-h*2/3, flagRGB(213, 43, 30))
	case "ID":
		c.rect(0, 0, w, h/2, flagRGB(255, 0, 0))
	case "IN":
		c.rect(0, 0, w, h/3, flagRGB(255, 153, 51))
		c.rect(0, h*2/3, w, h-h*2/3, flagRGB(18, 136, 7))
		navy := flagRGB(0, 0, 136)
		cx, cy, r := fw/2.0, fh/2.0, fh*2.0/15
		c.oval(cx-r, cy-r, 2*r, 2*r, navy)
		c.oval(cx-r*0.875, cy-r*0.875, r*1.75, r*1.75, flagWhite)
		c.oval(cx-r*0.175, cy-r*0.175, r*0.35, r*0.35, navy)
		px := [4]float64{0, 0.03, 0, -0.03}
		py := [4]float64{0.875, 0.35, 0.1, 0.25}
		for i := range 24 {
			sinA, cosA := math.Sincos(float64(i) * flagPi / 12)
			var p [4]flagPoint
			for j := range p {
				p[j] = flagPoint{cx + r*(px[j]*cosA-py[j]*sinA), cy + r*(px[j]*sinA+py[j]*cosA)}
			}
			c.poly(p[:], navy)
		}
	case "MY":
		navy, gold := flagRGB(0, 0, 102), flagRGB(255, 204, 0)
		for i := 0; i < 14; i += 2 {
			c.rect(0, i*h/14, w, (i+1)*h/14-i*h/14, flagRGB(204, 0, 0))
		}
		c.rect(0, 0, w/2, h*4/7, navy)
		scale := fh / 480.0
		cx, cy := fw*127.6/640, fh*140.5/480
		c.oval(cx-80*scale, cy-80*scale, 160*scale, 160*scale, gold)
		c.oval(cx+(17.6-71)*scale, cy-71*scale, 142*scale, 142*scale, navy)
		var star [28]flagPoint
		for i := range star {
			a := -flagPi/2 + float64(i)*flagPi/14
			r := 66.7 * scale
			if i%2 != 0 {
				r = 26.7 * scale
			}
			star[i] = flagPoint{fw*207.5/640 + r*math.Cos(a), cy + r*math.Sin(a)}
		}
		c.poly(star[:], gold)
	case "CA":
		red := flagRGB(213, 43, 30)
		c.rect(0, 0, w/4, h, red)
		c.rect(w*3/4, 0, w-w*3/4, h, red)
		// Eleven-point maple leaf, simplified from flag-icons.
		leaf := [29][2]float64{{201, 232}, {187.7, 236.4}, {249.1, 290.4}, {243.5, 315.4},
			{310.1, 307}, {308.5, 374}, {322.4, 373.7}, {319.3, 307.1}, {386, 315.1},
			{382, 287.9}, {443.3, 236.9}, {432.6, 232.9}, {438.2, 184}, {400.2, 189.8},
			{391, 172.3}, {358.4, 208.1}, {352.5, 204.6}, {367.5, 129.8}, {343.7, 143.2},
			{338.5, 141}, {315.5, 95}, {291.9, 142.8}, {286.9, 143.5}, {264, 130.8},
			{277.7, 204.9}, {271, 207.1}, {239.8, 171.8}, {227.6, 191.3}, {192, 184.3}}
		var p [29]flagPoint
		for i := range p {
			p[i] = flagPoint{fw/2.0 + (leaf[i][0]-315.5)*fh/480, leaf[i][1] * fh / 480}
		}
		c.poly(p[:], red)
	case "NL":
		c.rect(0, 0, w, h/3, flagRGB(174, 28, 40))
		c.rect(0, h*2/3, w, h-h*2/3, flagRGB(33, 70, 139))
	case "IE":
		c.rect(0, 0, w/3, h, flagRGB(22, 155, 98))
		c.rect(w*2/3, 0, w-w*2/3, h, flagRGB(255, 136, 62))
	case "CH":
		c.rect(0, 0, w, h, flagRGB(218, 41, 28))
		c.rect(w/2-h*5/16, h*13/32, h*10/16, h*6/32, flagWhite)
		c.rect(w/2-h*3/32, h*3/16, h*6/32, h*10/16, flagWhite)
	case "SE", "FI":
		sweden := code == "SE"
		bg, cross, thick := flagWhite, flagRGB(0, 47, 108), h*3/11
		if sweden {
			bg, cross, thick = flagRGB(0, 106, 167), flagRGB(254, 204, 0), h/5
		}
		c.rect(0, 0, w, h, bg)
		c.rect(0, (h-thick)/2, w, thick, cross)
		c.rect(w*3/8-thick/2, 0, thick, h, cross)
	case "JP":
		c.oval(fw*0.5-fh*0.3, fh*0.2, fh*0.6, fh*0.6, flagRGB(188, 0, 45))
	case "DE":
		c.rect(0, 0, w, h/3, flagBlack)
		c.rect(0, h/3, w, h*2/3-h/3, flagRGB(221, 0, 0))
		c.rect(0, h*2/3, w, h-h*2/3, flagRGB(255, 206, 0))
	case "FR":
		c.rect(0, 0, w/3, h, flagRGB(0, 38, 84))
		c.rect(w*2/3, 0, w-w*2/3, h, flagRGB(237, 41, 57))
	case "GB":
		c.unionJack()
	case "SG":
		red := flagRGB(239, 51, 64)
		c.rect(0, 0, w, h/2, red)
		c.oval(fw*0.075, fh*0.045, fh*0.41, fh*0.41, flagWhite)
		c.oval(fw*0.119, fh*0.045, fh*0.41, fh*0.41, red)
		// Five upright stars arranged in a pentagon inside the crescent opening.
		for i := range 5 {
			sinA, cosA := math.Sincos(-flagPi/2 + float64(i)*2*flagPi/5)
			c.star(fw*0.245+fh*0.115*cosA, fh*0.25+fh*0.115*sinA, fh*0.047, flagWhite, flagStarUp)
		}
	default:
		c.label(code)
	}
}
