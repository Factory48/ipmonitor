package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// The GTK/Win32 lifecycle must run on the process's main thread, not in a
// testing goroutine. Each child has its own app loop and user-data directory.
func TestMain(m *testing.M) {
	if mode := os.Getenv("IPMONITOR_NATIVE_CHILD"); mode != "" {
		if err := runNativeWindowScenario(mode); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeMainWindowCloseReopen(t *testing.T) {
	for _, mode := range []string{"fresh", "saved", "damaged"} {
		t.Run(mode, func(t *testing.T) { runNativeChild(t, mode) })
	}
}

func runNativeChild(t *testing.T, mode string) {
	t.Helper()
	if os.Getenv("IPMONITOR_NATIVE_TEST") != "1" {
		t.Skip("set IPMONITOR_NATIVE_TEST=1 in a desktop session")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("native app supports Linux and Windows")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "IPMONITOR_NATIVE_CHILD="+mode, "IPMONITOR_NATIVE_DIR="+t.TempDir())
	if runtime.GOOS == "linux" {
		cmd.Env = append(cmd.Env, "GDK_BACKEND=x11", "TZ=Asia/Shanghai")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native %s scenario: %v\n%s", mode, err, out)
	}
	t.Logf("%s", out)
}

func runNativeWindowScenario(mode string) error {
	dir := os.Getenv("IPMONITOR_NATIVE_DIR")
	mygo.App.SetName("IPMonitor window regression")
	mygo.App.SetPath(mygo.PathUserData, dir)
	// Keep clipboard monitoring paused in the isolated test app; no lookup
	// or user configuration is involved in exercising the real tray lifecycle.
	a := &application{language: "en", config: config{Language: "en", Token: "unused-test-token"}}
	result := make(chan error, 1)
	mygo.App.WhenReady(func() {
		area := mygo.Screen.PrimaryDisplay().WorkArea
		var want mygo.Rectangle
		path := filepath.Join(dir, "window-state.json")
		if mode == "saved" {
			want = mygo.Rectangle{X: area.X + 40, Y: area.Y + 60, Width: 700, Height: 400}
			data, err := json.Marshal(map[string]mygo.WindowState{"main": {
				X: want.X, Y: want.Y, Width: want.Width, Height: want.Height,
			}})
			if err == nil {
				err = os.WriteFile(path, data, 0o600)
			}
			if err != nil {
				result <- err
				mygo.App.Quit()
				return
			}
		} else if mode == "damaged" {
			if err := os.WriteFile(path, []byte("broken JSON"), 0o600); err != nil {
				result <- err
				mygo.App.Quit()
				return
			}
		}
		a.ready()
		go func() {
			var err error
			if mode == "timestamp" {
				err = exerciseTimestampClipboard(a)
			} else {
				err = exerciseNativeWindow(a, mode, area, want)
			}
			result <- err
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	if err := <-result; err != nil {
		return err
	}
	if !a.win.IsDestroyed() {
		return fmt.Errorf("Quit did not destroy the main window")
	}
	fmt.Println("quit destroyed the window")
	return nil
}

func exerciseNativeWindow(a *application, mode string, area, want mygo.Rectangle) error {
	w := a.win
	handle := w.NativeHandle()
	for cycle := range 20 {
		mygo.RunOnMain(a.openMain)
		if err := awaitNativeWindow("mapped after open", func() bool { return nativeWindowMapped(w) }); err != nil {
			return fmt.Errorf("cycle %d: %w", cycle, err)
		}
		if cycle == 0 {
			if mode == "saved" {
				if err := awaitNativeWindow("saved bounds restored", func() bool { return w.Bounds() == want }); err != nil {
					return fmt.Errorf("%w; actual=%+v expected=%+v", err, w.Bounds(), want)
				}
			} else {
				if err := awaitNativeWindow("fresh window centered", func() bool {
					b := w.Bounds()
					return absInt(2*b.X+b.Width-(2*area.X+area.Width)) <= 24 &&
						absInt(2*b.Y+b.Height-(2*area.Y+area.Height)) <= 24
				}); err != nil {
					return fmt.Errorf("%w; actual=%+v work-area=%+v", err, w.Bounds(), area)
				}
			}
			fmt.Printf("%s first open: bounds=%+v work-area=%+v\n", mode, w.Bounds(), area)
		}
		w.Close() // Same native close event as the title-bar close button.
		if w.IsDestroyed() || w.NativeHandle() != handle {
			return fmt.Errorf("cycle %d: Close destroyed the reusable native window", cycle)
		}
		if err := awaitNativeWindow("unmapped after close", func() bool { return !nativeWindowMapped(w) }); err != nil {
			return fmt.Errorf("cycle %d: %w", cycle, err)
		}
	}
	mygo.RunOnMain(a.openMain)
	if err := awaitNativeWindow("mapped on final reopen", func() bool { return nativeWindowMapped(w) }); err != nil {
		return err
	}
	if path := os.Getenv("IPMONITOR_NATIVE_CAPTURE"); path != "" && mode == "fresh" {
		png, err := w.CapturePage()
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, png, 0o600); err != nil {
			return err
		}
	}
	fmt.Println("20 close/reopen cycles kept the native window alive and mapped")
	return nil
}

func awaitNativeWindow(label string, condition func() bool) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", label)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
