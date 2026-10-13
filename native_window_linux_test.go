package main

import (
	"sync"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

var (
	nativeMappedOnce sync.Once
	gtkWidgetMapped  func(widget uintptr) bool
)

// GTK visibility is only a request; mapped proves the X11 window is showing.
func nativeWindowMapped(w *mygo.Window) bool {
	var mapped bool
	mygo.RunOnMain(func() {
		nativeMappedOnce.Do(func() {
			lib, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
			if err != nil {
				panic(err)
			}
			purego.RegisterLibFunc(&gtkWidgetMapped, lib, "gtk_widget_get_mapped")
		})
		if h := w.NativeHandle(); h != 0 {
			mapped = gtkWidgetMapped(h)
		}
	})
	return mapped
}
