package main

import (
	"syscall"

	"github.com/egoist/mygo"
)

var isWindowVisible = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")
var isWindowIconic = syscall.NewLazyDLL("user32.dll").NewProc("IsIconic")

func nativeWindowMapped(w *mygo.Window) bool {
	var mapped bool
	mygo.RunOnMain(func() {
		h := w.NativeHandle()
		if h != 0 {
			visible, _, _ := isWindowVisible.Call(h)
			iconic, _, _ := isWindowIconic.Call(h)
			mapped = visible != 0 && iconic == 0
		}
	})
	return mapped
}
