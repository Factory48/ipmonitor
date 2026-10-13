//go:build !linux && !windows

package main

import "github.com/egoist/mygo"

func nativeWindowMapped(w *mygo.Window) bool { return w.IsVisible() && !w.IsMinimized() }
