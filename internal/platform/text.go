// Package platform watches the system clipboard for text changes.
//
// WatchClipboard is the only entry point. It never reads the clipboard at
// start-up, never polls, and only accepts short text: at most maxTextUnits
// UTF-16 code units, without NUL. The size limit is checked on the native
// size or length before the data is copied wherever the OS API allows it.
package platform

import (
	"bytes"
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

// ErrUnsupported is returned by WatchClipboard on operating systems without a
// clipboard listener implementation.
var ErrUnsupported = errors.New("platform: clipboard watching is not supported on this OS")

const (
	// maxTextUnits is the longest accepted text in UTF-16 code units.
	maxTextUnits = 128
	// maxUTF8Bytes bounds maxTextUnits units in UTF-8: a BMP code unit takes
	// at most 3 bytes and a surrogate pair 4 bytes for 2 units.
	maxUTF8Bytes = maxTextUnits * 3
)

// textFromUTF8 returns b as a string when it is non-empty, valid UTF-8, free
// of NUL and at most maxTextUnits UTF-16 code units long.
func textFromUTF8(b []byte) (string, bool) {
	if len(b) == 0 || len(b) > maxUTF8Bytes || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return "", false
	}
	units := 0
	for rest := b; len(rest) > 0; {
		r, n := utf8.DecodeRune(rest)
		rest = rest[n:]
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		if units > maxTextUnits {
			return "", false
		}
	}
	return string(b), true
}

// textFromLatin1 decodes an ISO 8859-1 selection (the X11 STRING target).
func textFromLatin1(b []byte) (string, bool) {
	if len(b) == 0 || len(b) > maxTextUnits || bytes.IndexByte(b, 0) >= 0 {
		return "", false
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes), true
}

// textFromUTF16 decodes u, which excludes the terminator. It rejects empty
// text, more than maxTextUnits units, NUL and unpaired surrogates.
func textFromUTF16(u []uint16) (string, bool) {
	if len(u) == 0 || len(u) > maxTextUnits {
		return "", false
	}
	for i := 0; i < len(u); i++ {
		switch c := u[i]; {
		case c == 0:
			return "", false
		case c >= 0xD800 && c < 0xDC00:
			if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				return "", false
			}
			i++
		case c >= 0xDC00 && c <= 0xDFFF:
			return "", false
		}
	}
	return string(utf16.Decode(u)), true
}
