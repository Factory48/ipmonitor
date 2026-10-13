// Package core parses clipboard IP literals and timestamps, applies the address
// scope policy and performs IPinfo Lite lookups.
package core

import (
	"errors"
	"net/netip"
	"strings"
)

const (
	// MaxInputUnits is the largest accepted input, counted in UTF-16 code
	// units (the unit of the original clipboard limit), not bytes or runes.
	MaxInputUnits = 128
	// MaxIPChars is the longest IP literal ever parsed (45 characters).
	MaxIPChars = 45
)

// ErrInvalidIP is returned when the text does not start with an acceptable IP.
var ErrInvalidIP = errors.New("invalid IP address")

// ParseIP extracts only the leading IP of text, optionally preceded by ASCII
// whitespace and a case-insensitive http:// or https://. After four valid
// decimal IPv4 octets (at most three digits each, no leading zeros) any
// suffix is ignored, e.g. "122.117.5.2_8088_.toml" is 122.117.5.2, while
// "122.117.5.288888" is rejected. IPv6 with a port must be bracketed; an
// unbracketed string that is itself valid IPv6 is an address, not a port.
// Zones ("%eth0") are rejected. The whole input, including the ignored
// suffix, is limited to 128 UTF-16 units and must not contain NUL.
// IPv4-mapped IPv6 addresses are returned as IPv6 (not unmapped).
func ParseIP(text string) (netip.Addr, error) {
	if addr, ok := parseIP(text); ok {
		return addr, nil
	}
	return netip.Addr{}, ErrInvalidIP
}

func parseIP(text string) (netip.Addr, bool) {
	if exceedsUnits(text, MaxInputUnits) || strings.IndexByte(text, 0) >= 0 {
		return netip.Addr{}, false
	}
	for text != "" && isSpace(text[0]) {
		text = text[1:]
	}
	if hasPrefixFold(text, "https://") {
		text = text[8:]
	} else if hasPrefixFold(text, "http://") {
		text = text[7:]
	}
	if text == "" {
		return netip.Addr{}, false
	}

	// Bracketed IPv6, optionally followed by a boundary such as ":port".
	if text[0] == '[' {
		end := 1
		for end < len(text) && end <= MaxIPChars && text[end] != ']' {
			end++
		}
		if end >= len(text) || text[end] != ']' {
			return netip.Addr{}, false
		}
		if end+1 < len(text) && !isBoundary(text[end+1], true) {
			return netip.Addr{}, false
		}
		return parseV6(text[1:end])
	}

	// IPv4: exactly four decimal octets of at most three digits; the rest is
	// ignored. A fourth octet followed by another digit is an error, not a
	// shorter address.
	end := 0
	v4 := true
	for octet := range 4 {
		begin := end
		for end < len(text) && end-begin < 3 && isDigit(text[end]) {
			end++
		}
		if end == begin {
			v4 = false
			break
		}
		if end < len(text) && isDigit(text[end]) {
			if octet == 3 {
				return netip.Addr{}, false
			}
			v4 = false // may still be IPv6, e.g. 2001::1
			break
		}
		if octet < 3 {
			if end >= len(text) || text[end] != '.' {
				v4 = false
				break
			}
			end++
		}
	}
	if v4 {
		addr, err := netip.ParseAddr(text[:end])
		if err != nil || !addr.Is4() {
			return netip.Addr{}, false
		}
		return addr, true
	}

	end = 0
	for end < len(text) && end <= MaxIPChars && isV6Char(text[end]) {
		end++
	}
	if end > MaxIPChars || (end < len(text) && !isBoundary(text[end], false)) {
		return netip.Addr{}, false
	}
	return parseV6(text[:end])
}

func parseV6(s string) (netip.Addr, bool) {
	if s == "" || len(s) > MaxIPChars || strings.IndexByte(s, '%') >= 0 {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is6() {
		return netip.Addr{}, false
	}
	return addr, true
}

// exceedsUnits reports whether s is longer than limit UTF-16 code units.
func exceedsUnits(s string, limit int) bool {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
		if n > limit {
			return true
		}
	}
	return false
}

func isSpace(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isV6Char(c byte) bool {
	return c == ':' || c == '.' || isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

func isBoundary(c byte, port bool) bool {
	return c == '/' || c == '?' || c == '#' || isSpace(c) || (port && c == ':')
}

// hasPrefixFold compares ASCII case-insensitively; prefix must be lower case.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range len(prefix) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// IsPublic is an offline address-scope policy, not a guarantee of Internet
// routability. IPv4-mapped IPv6 addresses use the IPv4 policy. IPv6 is
// conservative: only 2000::/3, minus 2001::/23 (including its globally
// reachable exceptions), 2001:db8::/32, 2002::/16 and 3fff::/20. Invalid and
// zoned addresses are not public.
func IsPublic(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is4() {
		return publicV4(addr.As4())
	}
	b := addr.As16()
	if b[0]&0xe0 != 0x20 {
		return false // not global unicast 2000::/3
	}
	if b[0] == 0x20 && b[1] == 0x01 && b[2] < 2 {
		return false // 2001::/23 special protocols
	}
	if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 {
		return false // documentation 2001:db8::/32
	}
	if b[0] == 0x3f && b[1] == 0xff && b[2]&0xf0 == 0 {
		return false // documentation 3fff::/20
	}
	if b[0] == 0x20 && b[1] == 0x02 {
		return false // deprecated 6to4 2002::/16
	}
	return true
}

func publicV4(b [4]byte) bool {
	if b[0] == 0 || b[0] == 10 || b[0] == 127 || b[0] >= 224 {
		return false
	}
	switch {
	case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // CGNAT
		return false
	case b[0] == 169 && b[1] == 254:
		return false
	case b[0] == 172 && b[1] >= 16 && b[1] <= 31:
		return false
	case b[0] == 192:
		if b[1] == 168 {
			return false
		}
		if b[1] == 0 && b[2] == 0 { // IETF protocol assignments; PCP/TURN anycast are global
			return b[3] == 9 || b[3] == 10
		}
		if b[1] == 0 && b[2] == 2 { // TEST-NET-1
			return false
		}
		if b[1] == 88 && b[2] == 99 { // deprecated 6to4 relay
			return false
		}
	case b[0] == 198:
		if b[1] == 18 || b[1] == 19 { // benchmarking
			return false
		}
		if b[1] == 51 && b[2] == 100 { // TEST-NET-2
			return false
		}
	case b[0] == 203 && b[1] == 0 && b[2] == 113: // TEST-NET-3
		return false
	}
	return true
}
