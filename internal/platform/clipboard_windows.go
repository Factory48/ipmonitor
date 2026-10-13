//go:build windows && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Everything here runs on the thread that called WatchClipboard (the MyGo UI
// thread): the message-only window belongs to it and MyGo's own message loop
// dispatches to winProc. No locks guard the registries for that reason.

const (
	wmTimer           = 0x0113
	wmClipboardUpdate = 0x031D

	cfUnicodeText = 13

	// maxTextBytes is 128 UTF-16 units plus the NUL terminator.
	maxTextBytes = (maxTextUnits + 1) * 2

	retryDelayMS = 20
	maxRetries   = 3
	retryTimerID = 1

	winClassName = "IPMonitorClipboardWatcher"
)

var hwndMessage = ^uintptr(0) - 2 // HWND_MESSAGE, (HWND)-3

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll") // a KnownDLL
	user32   = syscall.NewLazyDLL(systemDirectory() + `\user32.dll`)

	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	procGlobalLock                    = kernel32.NewProc("GlobalLock")
	procGlobalUnlock                  = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                    = kernel32.NewProc("GlobalSize")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procUnregisterClassW              = user32.NewProc("UnregisterClassW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procAddClipboardFormatListener    = user32.NewProc("AddClipboardFormatListener")
	procRemoveClipboardFormatListener = user32.NewProc("RemoveClipboardFormatListener")
	procGetClipboardSequenceNumber    = user32.NewProc("GetClipboardSequenceNumber")
	procIsClipboardFormatAvailable    = user32.NewProc("IsClipboardFormatAvailable")
	procOpenClipboard                 = user32.NewProc("OpenClipboard")
	procCloseClipboard                = user32.NewProc("CloseClipboard")
	procGetClipboardData              = user32.NewProc("GetClipboardData")
	procSetTimer                      = user32.NewProc("SetTimer")
	procKillTimer                     = user32.NewProc("KillTimer")
)

func systemDirectory() string {
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, _ := kernel32.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 || int(n) > len(buf) {
		return `C:\Windows\System32`
	}
	return syscall.UTF16ToString(buf[:n])
}

type winWatcher struct {
	onText  func(string)
	hwnd    uintptr
	lastSeq uint32 // sequence number of the last change handled
	tries   int    // retries used for the current change
	timer   bool   // a retry timer is set
	stopped bool
}

var (
	winProcCallback = syscall.NewCallback(winProc) // created once, never freed
	winWatchers     = map[uintptr]*winWatcher{}
	winClassUsers   int
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s) // s has no NUL
	return p
}

func moduleHandle() uintptr {
	h, _, _ := procGetModuleHandleW.Call(0)
	return h
}

func winProc(hwnd, m, wparam, lparam uintptr) uintptr {
	if w := winWatchers[hwnd]; w != nil && !w.stopped {
		switch m {
		case wmClipboardUpdate:
			w.changed()
			return 0
		case wmTimer:
			if wparam == retryTimerID {
				w.killTimer()
				w.attempt()
				return 0
			}
		}
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return r
}

func (w *winWatcher) killTimer() {
	if w.timer {
		w.timer = false
		procKillTimer.Call(w.hwnd, retryTimerID)
	}
}

// changed handles WM_CLIPBOARDUPDATE. Windows posts one right after the
// listener is added; its sequence number is the one taken before, so the
// clipboard content present at start is never read.
func (w *winWatcher) changed() {
	seq := clipboardSequence()
	if seq == w.lastSeq {
		return
	}
	w.lastSeq = seq
	w.killTimer()
	w.tries = 0
	w.attempt()
}

func clipboardSequence() uint32 {
	n, _, _ := procGetClipboardSequenceNumber.Call()
	return uint32(n)
}

// attempt reads the clipboard once and sets a retry timer on a failure that
// can be temporary: the clipboard is held by another process or the data is
// not ready.
func (w *winWatcher) attempt() {
	text, status := w.read()
	switch status {
	case readOK:
		// The clipboard is closed; the callback may call stop.
		w.onText(text)
	case readRetry:
		if w.tries >= maxRetries {
			return
		}
		w.tries++
		if r, _, _ := procSetTimer.Call(w.hwnd, retryTimerID, retryDelayMS, 0); r != 0 {
			w.timer = true
		}
	}
}

type readStatus int

const (
	readIgnore readStatus = iota // not text, or text we do not accept
	readRetry
	readOK
)

func (w *winWatcher) read() (string, readStatus) {
	if ok, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); ok == 0 {
		return "", readIgnore
	}
	if ok, _, _ := procOpenClipboard.Call(w.hwnd); ok == 0 {
		return "", readRetry
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", readRetry
	}
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 {
		return "", readRetry
	}
	if size > maxTextBytes {
		return "", readIgnore // too large: nothing is locked or copied
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", readRetry
	}
	defer procGlobalUnlock.Call(h)
	var units [maxTextUnits + 1]uint16
	n := int(size) / 2
	copy(units[:n], unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n))
	end := 0
	for end < n && units[end] != 0 {
		end++
	}
	if end == n { // no terminator within the block
		return "", readIgnore
	}
	text, ok := textFromUTF16(units[:end])
	if !ok {
		return "", readIgnore
	}
	return text, readOK
}

func registerClass() error {
	if winClassUsers > 0 {
		winClassUsers++
		return nil
	}
	wc := wndClassEx{
		WndProc:   winProcCallback,
		Instance:  moduleHandle(),
		ClassName: utf16Ptr(winClassName),
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("platform: cannot register the clipboard window class: %w", err)
	}
	winClassUsers = 1
	return nil
}

func releaseClass() {
	if winClassUsers--; winClassUsers == 0 {
		procUnregisterClassW.Call(uintptr(unsafe.Pointer(utf16Ptr(winClassName))), moduleHandle())
	}
}

// WatchClipboard calls onText with the Unicode text of every later clipboard
// change. It must be called on the UI thread, whose message loop dispatches
// the notifications; onText runs on that thread and has to return quickly.
// The clipboard is neither read now nor polled. stop must run on the same
// thread, is idempotent, and no text is delivered once it returned.
func WatchClipboard(onText func(string)) (stop func(), err error) {
	if onText == nil {
		return nil, errors.New("platform: nil clipboard callback")
	}
	if err := registerClass(); err != nil {
		return nil, err
	}
	hwnd, _, cerr := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr(winClassName))), 0, 0, 0, 0, 0, 0,
		hwndMessage, 0, moduleHandle(), 0)
	if hwnd == 0 {
		releaseClass()
		return nil, fmt.Errorf("platform: cannot create the clipboard window: %w", cerr)
	}
	w := &winWatcher{onText: onText, hwnd: hwnd, lastSeq: clipboardSequence()}
	winWatchers[hwnd] = w
	if r, _, lerr := procAddClipboardFormatListener.Call(hwnd); r == 0 {
		delete(winWatchers, hwnd)
		procDestroyWindow.Call(hwnd)
		releaseClass()
		return nil, fmt.Errorf("platform: cannot listen to the clipboard: %w", lerr)
	}
	return func() {
		if w.stopped {
			return
		}
		w.stopped = true
		w.killTimer()
		procRemoveClipboardFormatListener.Call(w.hwnd)
		delete(winWatchers, w.hwnd)
		procDestroyWindow.Call(w.hwnd)
		w.onText = nil
		releaseClass()
	}, nil
}
