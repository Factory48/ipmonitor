package main

import (
	"github.com/Factory48/ipmonitor/internal/platform"
	"github.com/egoist/mygo"
)

const (
	popupWidth  = 380
	popupHeight = 144
	popupGap    = 8
)

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// positionPopup follows the actual notification-area icon where the backend
// exposes it. AppIndicator cannot expose an icon rectangle, so on Linux we
// locate the real X11 dock panel and place the card at its right edge; if the
// compositor owns the panel, the display work-area edge is the only honest
// fallback and is still near the taskbar rather than centered.
func (a *application) positionPopup() {
	if a.popup == nil {
		return
	}
	display := mygo.Screen.PrimaryDisplay()
	area := display.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		return
	}
	anchor := mygo.Rectangle{}
	if a.tray != nil {
		anchor = a.tray.Bounds()
	}
	if anchor.Width > 0 && anchor.Height > 0 {
		d := mygo.Screen.DisplayNearestPoint(mygo.Point{X: anchor.X + anchor.Width/2, Y: anchor.Y + anchor.Height/2})
		area = d.WorkArea
		width, height := a.sizePopupForArea(area)
		x := anchor.X + (anchor.Width-width)/2
		y := anchor.Y + anchor.Height + popupGap
		if y+height > area.Y+area.Height {
			y = anchor.Y - height - popupGap
		}
		x = clampInt(x, area.X, area.X+area.Width-width)
		y = clampInt(y, area.Y, area.Y+area.Height-height)
		a.popup.SetPosition(x, y)
		return
	}

	width, height := a.sizePopupForArea(area)
	panel, hasPanel := platform.PanelBounds(platform.Rect{
		X: display.Bounds.X, Y: display.Bounds.Y, Width: display.Bounds.Width, Height: display.Bounds.Height,
	})
	x := area.X + area.Width - width - 12
	y := area.Y + area.Height - height - 12
	if hasPanel {
		horizontal := panel.Width >= panel.Height
		if horizontal {
			x = area.X + area.Width - width - 12
			if panel.Y <= display.Bounds.Y+(display.Bounds.Height/2) {
				y = panel.Y + panel.Height + popupGap
			} else {
				y = panel.Y - height - popupGap
			}
		} else if panel.X <= display.Bounds.X+(display.Bounds.Width/2) {
			x = panel.X + panel.Width + popupGap
			y = area.Y + area.Height - height - 12
		} else {
			x = panel.X - width - popupGap
			y = area.Y + area.Height - height - 12
		}
	}
	a.popup.SetPosition(clampInt(x, area.X, area.X+area.Width-width), clampInt(y, area.Y, area.Y+area.Height-height))
}

func (a *application) sizePopupForArea(area mygo.Rectangle) (int, int) {
	width := min(popupWidth, max(1, area.Width-2*popupGap))
	height := min(popupHeight, max(1, area.Height-2*popupGap))
	if b := a.popup.Bounds(); b.Width != width || b.Height != height {
		a.popup.SetSize(width, height)
	}
	return width, height
}
