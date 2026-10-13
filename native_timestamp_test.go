package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/Factory48/ipmonitor/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

func TestNativeTimestampClipboardPopup(t *testing.T) {
	runNativeChild(t, "timestamp")
}

func exerciseTimestampClipboard(a *application) error {
	seed := []historyEntry{{
		Info:    core.Info{IP: "8.8.8.8", CountryCode: "US", Country: "United States", ASName: "Google LLC"},
		Fetched: time.Now(), Seen: time.Now(),
	}}
	var oldID uint64
	var watching bool
	mygo.RunOnMain(func() {
		a.entries = append([]historyEntry(nil), seed...)
		a.config.Token = "" // Timestamp display must not require an IPinfo token.
		a.setWatching(true)
		a.win.Hide()
		oldID, watching = a.latestID, a.watching
	})
	if !watching {
		return fmt.Errorf("clipboard monitoring unavailable")
	}

	sample := time.Date(2026, 10, 13, 8, 34, 15, 533000000, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
	}{
		{"sample", sample},
		{"past", time.Now().Add(-time.Hour)},
		{"future", time.Now().Add(time.Minute)},
	} {
		if err := writeTimestampAndAwait(a, tc.at); err != nil {
			return err
		}
		if err := captureTimestampColor(a, !time.Now().Before(tc.at), tc.name); err != nil {
			return err
		}
		fmt.Printf("%s: %s -> %s (%s)\n", tc.name, strconv.FormatInt(tc.at.UnixMilli(), 10), tc.at.Local().Format("2006-01-02 15:04:05"), time.Local)
		if tc.name == "sample" {
			mygo.RunOnMain(func() {
				a.handleDone(lookupDone{Job: lookupJob{IP: "1.1.1.1", ID: oldID, Notify: true}, Info: core.Info{IP: "1.1.1.1"}})
			})
		}
		var unchanged bool
		mygo.RunOnMain(func() {
			unchanged = reflect.DeepEqual(a.entries, seed) && a.query == "" && a.pending == nil && a.popupDeadline.UnixMilli() == tc.at.UnixMilli()
		})
		if !unchanged {
			return fmt.Errorf("timestamp altered history/cache/query or was overwritten by a stale IP lookup")
		}
	}

	deadline := time.UnixMilli(time.Now().Add(2 * time.Second).UnixMilli())
	if err := writeTimestampAndAwait(a, deadline); err != nil {
		return err
	}
	if err := captureTimestampColor(a, false, "before-expiry"); err != nil {
		return err
	}
	time.Sleep(time.Until(deadline) + 100*time.Millisecond)
	if err := captureTimestampColor(a, true, "after-expiry"); err != nil {
		return err
	}
	fmt.Println("visible timestamp switched from green to red at expiry")

	// The same popup must return to normal IP rendering, using the existing cache.
	if err := mygo.Clipboard.Write(transfer.TextData("8.8.8.8")); err != nil {
		return err
	}
	if err := awaitNativeWindow("cached IP popup after timestamp", func() bool {
		var normal bool
		mygo.RunOnMain(func() {
			normal = a.popupTitle == "8.8.8.8" && a.popupCode == "US" && a.popupDeadline.IsZero() && nativeWindowMapped(a.popup)
		})
		return normal
	}); err != nil {
		return err
	}
	if err := capturePopupExamples(a); err != nil {
		return err
	}
	fmt.Println("timestamp kept history/cache unchanged without a token; IP popup still works")
	return nil
}

func writeTimestampAndAwait(a *application, at time.Time) error {
	text := strconv.FormatInt(at.UnixMilli(), 10)
	if _, ok := core.ParseTimestamp(text); !ok {
		return fmt.Errorf("native test clock is outside the supported 17-prefix range: %s", text)
	}
	if err := mygo.Clipboard.Write(transfer.TextData(text)); err != nil {
		return err
	}
	return awaitNativeWindow("timestamp clipboard popup", func() bool {
		var shown bool
		mygo.RunOnMain(func() {
			shown = a.popupDeadline.UnixMilli() == at.UnixMilli() && a.popupTitle == at.Local().Format("2006-01-02 15:04:05") && a.popupCode == "" && nativeWindowMapped(a.popup)
		})
		return shown
	})
}

func captureTimestampColor(a *application, expired bool, name string) error {
	var shot []byte
	var captureErr error
	err := awaitNativeWindow("timestamp expiry color", func() bool {
		shot, captureErr = a.popup.CapturePage()
		if captureErr != nil {
			return true
		}
		img, err := png.Decode(bytes.NewReader(shot))
		if err != nil {
			captureErr = err
			return true
		}
		red, green := 0, 0
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				r, g, b = r>>8, g>>8, b>>8
				if r > g+30 && r > b+30 {
					red++
				}
				if g > r+20 && g > b+10 {
					green++
				}
			}
		}
		if expired {
			return red > 10 && red > green
		}
		return green > 10 && green > red
	})
	if captureErr != nil {
		return captureErr
	}
	if err != nil {
		return fmt.Errorf("%s expired=%v: %w", name, expired, err)
	}
	if base := os.Getenv("IPMONITOR_TIMESTAMP_CAPTURE"); base != "" {
		return os.WriteFile(base+"-"+name+".png", shot, 0o600)
	}
	return nil
}

func capturePopupExamples(a *application) error {
	base := os.Getenv("IPMONITOR_TIMESTAMP_CAPTURE")
	if base == "" {
		return nil
	}
	for _, example := range []struct {
		name, title, text, code string
		width, height           int
	}{
		{name: "ip"},
		{name: "ipv6", title: "2606:4700:4700:ffff:ffff:ffff:ffff:ffff", text: "United States (US)\nCloudflare, Inc.", code: "US"},
		{name: "cjk", title: "1.2.4.8", text: "中国 (CN)\n中国互联网信息中心 / 长名称自治系统服务提供商示例", code: "CN"},
		{name: "small", width: 320, height: 120},
		{name: "large", width: 456, height: 173},
	} {
		mygo.RunOnMain(func() {
			if example.title != "" {
				a.showPopup(example.title, example.text, example.code, true)
			}
			if example.width != 0 {
				a.showTimestampPopup("1791880455533", time.UnixMilli(1791880455533))
				a.popup.SetSize(example.width, example.height)
				a.popup.Update(func() {})
			}
		})
		// Capture the displayed frame, not the previous frame awaiting repaint.
		time.Sleep(120 * time.Millisecond)
		shot, err := a.popup.CapturePage()
		if err != nil {
			return err
		}
		if err := os.WriteFile(base+"-"+example.name+".png", shot, 0o600); err != nil {
			return err
		}
	}
	return nil
}
