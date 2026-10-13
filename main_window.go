package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *application) createMainWindow() {
	// Set an explicit default position before GTK realizes the hidden window.
	// GTK's automatic centering can use the hidden widget's natural size, not
	// the requested 820x500 size. StateKey overrides this only for saved state.
	area := mygo.Screen.PrimaryDisplay().WorkArea
	width, height := 820, 500
	if area.Width > 0 && area.Height > 0 {
		width, height = min(width, area.Width), min(height, area.Height)
	}
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title: appName + " - " + a.tr("查询与历史", "Lookup and history"),
		Width: width, Height: height,
		X: area.X + (area.Width-width)/2, Y: area.Y + (area.Height-height)/2,
		MinWidth: min(650, width), MinHeight: min(360, height),
		StateKey: "main", Hidden: true, Content: ui.View(a.view),
	})
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			e.Window.Hide()
		}
	})
}

func (a *application) openMain() {
	if a.quitting || a.win == nil || a.win.IsDestroyed() {
		return
	}
	if a.popup != nil {
		a.popup.Hide()
	}
	a.win.Restore()
	a.win.Show()
	a.win.Focus()
	mygo.App.Focus()
}
