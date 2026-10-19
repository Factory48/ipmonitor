//go:build linux

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// loginArgument is the argument mygo adds to the command that starts the
// app at login; it separates the app's autostart entry from other entries.
const loginArgument = "--mygo-opened-at-login"

// runningExecutable is the file an autostart entry has to launch: the
// AppImage, else the running executable with symlinks resolved, as mygo
// records it.
func runningExecutable() (string, error) {
	if appImage := os.Getenv("APPIMAGE"); appImage != "" {
		return appImage, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// autostartDir is the XDG directory of the user's autostart entries.
func autostartDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "autostart")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "autostart")
}

// entryRunsExecutable reports whether an autostart entry of this app
// starts exe. name is the app name mygo writes into the entry.
func entryRunsExecutable(dir, name, exe string) bool {
	entries, err := filepath.Glob(filepath.Join(dir, "*.desktop"))
	if err != nil {
		return false
	}
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(data)
		if !strings.Contains(text, "\nName="+name+"\n") || !strings.Contains(text, loginArgument) {
			continue
		}
		if strings.Contains(text, exe) {
			return true
		}
	}
	return false
}

// ensureAutostart points an enabled autostart entry at the executable
// that is running. mygo writes the path only when the setting is turned
// on, so moving the app to another location (for instance from /opt to the
// package's /usr/bin) leaves an enabled entry naming a file that no longer
// exists: the app silently stops starting at login.
func ensureAutostart() {
	if !mygo.App.OpenAtLogin() {
		return
	}
	exe, err := runningExecutable()
	if err != nil {
		return
	}
	if entryRunsExecutable(autostartDir(), mygo.App.Name(), exe) {
		return
	}
	if err := mygo.App.SetOpenAtLogin(true); err != nil {
		log.Printf("autostart entry: %v", err)
	}
}
