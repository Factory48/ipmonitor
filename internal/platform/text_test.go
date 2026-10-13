package platform

import (
	"strings"
	"testing"
)

func TestClipboardTextBounds(t *testing.T) {
	if got, ok := textFromUTF8([]byte("8.8.8.8")); !ok || got != "8.8.8.8" {
		t.Fatalf("valid text = %q, %v", got, ok)
	}
	if _, ok := textFromUTF8([]byte("\x00")); ok {
		t.Fatal("NUL text accepted")
	}
	if _, ok := textFromUTF8([]byte("\xff")); ok {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, ok := textFromUTF8([]byte("😀" + strings.Repeat("x", maxUTF8Bytes))); ok {
		t.Fatal("oversized text accepted")
	}
	if got, ok := textFromUTF16([]uint16{'8', '.', '8', '.', '8', '.', '8'}); !ok || got != "8.8.8.8" {
		t.Fatalf("valid UTF-16 = %q, %v", got, ok)
	}
	if _, ok := textFromUTF16([]uint16{0xd800}); ok {
		t.Fatal("unpaired surrogate accepted")
	}
}
