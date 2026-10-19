//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEntryRunsExecutable(t *testing.T) {
	const exe = "/usr/bin/ipmonitor"
	entries := map[string]string{
		"current.desktop": "[Desktop Entry]\nType=Application\nName=IPMonitor\nExec=\"" + exe + "\" " + loginArgument + "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n",
		"moved.desktop":   "[Desktop Entry]\nType=Application\nName=IPMonitor\nExec=\"/opt/ipmonitor/ipmonitor\" " + loginArgument + "\nTerminal=false\nX-GNOME-Autostart-enabled=true\n",
		"other.desktop":   "[Desktop Entry]\nType=Application\nName=IPMonitor\nExec=\"" + exe + "\"\nTerminal=false\n",
	}
	for _, tc := range []struct {
		name string
		file string
		want bool
	}{
		{"current path", "current.desktop", true},
		{"path of a previous install", "moved.desktop", false},
		{"entry that is not the login entry", "other.desktop", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte(entries[tc.file]), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := entryRunsExecutable(dir, "IPMonitor", exe); got != tc.want {
				t.Fatalf("entryRunsExecutable(%q) = %v, want %v", tc.file, got, tc.want)
			}
		})
	}
}

func TestEntryRunsExecutableIgnoresOtherApps(t *testing.T) {
	dir := t.TempDir()
	entry := "[Desktop Entry]\nName=Other\nExec=\"/usr/bin/ipmonitor\" " + loginArgument + "\n"
	if err := os.WriteFile(filepath.Join(dir, "other.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	if entryRunsExecutable(dir, "IPMonitor", "/usr/bin/ipmonitor") {
		t.Fatal("an entry of another app matched")
	}
}
