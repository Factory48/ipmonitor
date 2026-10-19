//go:build !linux

package main

// ensureAutostart is a no-op: Windows checks the recorded command itself,
// and macOS login items and launch agents follow the app bundle.
func ensureAutostart() {}
