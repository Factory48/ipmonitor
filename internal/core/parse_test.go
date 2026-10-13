package core

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestParseIPAccepts(t *testing.T) {
	maxV6 := "ffff:ffff:ffff:ffff:ffff:ffff:255.255.255.255"
	if len(maxV6) != MaxIPChars {
		t.Fatalf("fixture length %d", len(maxV6))
	}
	cases := map[string]string{
		"0.0.0.0":                         "0.0.0.0",
		" \t\r\n\v\f192.0.2.1\f\v\n\r\t ": "192.0.2.1",
		"HtTp://192.0.2.1":                "192.0.2.1",
		" \tHTTPS://2001:0DB8:0000:0000:0000:0000:0000:0001\n": "2001:db8::1",
		"::":                             "::",
		"::FFFF:192.0.2.128":             "::ffff:192.0.2.128",
		"http://::ffff:192.0.2.128":      "::ffff:192.0.2.128",
		"2001:db8::1:443":                "2001:db8::1:443", // valid IPv6, not a port
		"122.117.5.2_8088_.toml":         "122.117.5.2",     // suffix after 4 octets ignored
		"https://122.117.5.2_8088_.toml": "122.117.5.2",
		"8.8.8.8evil":                    "8.8.8.8",
		"8.8.8.8.example.com":            "8.8.8.8",
		"8.8.8.8@host":                   "8.8.8.8",
		"8.8.8.8%12":                     "8.8.8.8",
		"8.8.8.8\u2003x":                 "8.8.8.8",
		"192.0.2.1:80":                   "192.0.2.1",
		"192.0.2.1 192.0.2.2":            "192.0.2.1",
		"192.0.2.1\n::1":                 "192.0.2.1",
		"[::1]":                          "::1",
		"https://[::1]":                  "::1",
		"[::1]:443":                      "::1",
		"[2001:db8::1]:not-a-port/path":  "2001:db8::1",
		"2001:db8::1/path":               "2001:db8::1",
		"2001:db8::1?q":                  "2001:db8::1",
		"2001:db8::1 \t8.8.8.8":          "2001:db8::1",
		"255.255.255.255:443/":           "255.255.255.255",
		maxV6 + "/ignored":               maxV6,
		"[" + maxV6 + "]:443/":           maxV6,
	}
	for in, want := range cases {
		got, err := ParseIP(in)
		if err != nil || got != netip.MustParseAddr(want) {
			t.Errorf("ParseIP(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
}

func TestParseIPRejects(t *testing.T) {
	maxV6 := "ffff:ffff:ffff:ffff:ffff:ffff:255.255.255.255"
	bad := []string{
		"", " \t\r\n", "http://", "https://", "example.com", "evil", "ftp://192.0.2.1",
		"http://https://192.0.2.1", "http:// 192.0.2.1", "https://\t::1",
		"user@192.0.2.1", "https://example.com/8.8.8.8", "IP:8.8.8.8", "text 8.8.8.8",
		"8.8.8.888", "8.8.8.888/", "122.117.5.288888", "255.255.255.2555:80",
		"[8.8.8.8]:443", "[::1", "::1]", "[[::1]]", "[::1]evil", "[::1].example.com",
		"[::1]@host", "[::1]%12", "[::1]\u2003", "[::1 ]", "[]",
		"fe80::1%12", "fe80::1%eth0", "[fe80::1%eth0]:443", "https://[fe80::1%12]/",
		"2001:db8::1evil", "2001:db8::1.example.com", "2001:db8::1\u2003",
		"192.0. 2.1", "256.0.0.1", "1.2.3", "127.1", "2130706433", "0x7f000001",
		"01.2.3.4", "+1.2.3.4", "-1.2.3.4", "1..2.3",
		":", ":::1", "1::2::3", "12345::", "gggg::1", "1:2:3:4:5:6:7", "1:2:3:4:5:6:7:8:9",
		"::ffff:256.1.2.3", "::ffff:192.0.2.1:80",
		"\u00a0192.0.2.1", "\uff11.2.3.4",
		maxV6 + "5/", "[" + maxV6 + "5]:443", strings.Repeat("1", 46),
		"192.0.2.1\x00junk", "::1\x00", "8.8.8.8/\x00ignored", "https://[::1]:443/\x00x",
	}
	for _, in := range bad {
		if got, err := ParseIP(in); !errors.Is(err, ErrInvalidIP) || got.IsValid() {
			t.Errorf("ParseIP(%q) = %v, %v; want ErrInvalidIP", in, got, err)
		}
	}
}

// The 128 limit counts UTF-16 units of the whole input, ignored suffix included.
func TestParseIPLengthIsUTF16Units(t *testing.T) {
	ok := []string{
		strings.Repeat(" ", 119) + "192.0.2.1",
		"https://8.8.8.8/" + strings.Repeat("x", 112),
		"8.8.8.8/" + strings.Repeat("😀", 60), // 128 units, only 68 runes
		"8.8.8.8 " + strings.Repeat("中", 50), // 58 units, 158 bytes
	}
	for _, in := range ok {
		if _, err := ParseIP(in); err != nil {
			t.Errorf("ParseIP(len %d bytes) rejected: %v", len(in), err)
		}
	}
	bad := []string{
		strings.Repeat(" ", 120) + "192.0.2.1",
		"https://8.8.8.8/" + strings.Repeat("x", 113),
		"8.8.8.8/" + strings.Repeat("😀", 61), // 130 units, but only 69 runes
		strings.Repeat(" ", 10000),
	}
	for _, in := range bad {
		if _, err := ParseIP(in); !errors.Is(err, ErrInvalidIP) {
			t.Errorf("ParseIP(len %d bytes) = %v; want ErrInvalidIP", len(in), err)
		}
	}
}

func TestIsPublicBoundaries(t *testing.T) {
	public := []string{
		"8.8.8.8", "1.1.1.1", "172.10.0.1", "172.15.255.255", "172.32.0.0",
		"100.63.255.255", "100.128.0.0", "9.255.255.255", "11.0.0.0", "126.255.255.255",
		"128.0.0.0", "169.253.255.255", "169.255.0.0", "192.167.255.255", "192.169.0.0",
		"192.0.0.9", "192.0.0.10", "192.0.1.255", "192.0.3.0", "198.17.255.255",
		"198.20.0.0", "198.51.99.255", "198.51.101.0", "203.0.112.255", "203.0.114.0",
		"223.255.255.255", "2606:4700:4700::1111", "2001:4860:4860::8888", "2001:200::1",
		"2001:db7:ffff:ffff:ffff:ffff:ffff:ffff", "2001:db9::", "2003::", "3fff:1000::",
		"::ffff:8.8.8.8", "::ffff:808:808", "::ffff:172.10.0.1", "HTTPS://::FFFF:1.1.1.1",
		"[2606:4700:4700::1111]:443", "8.8.8.8 192.168.1.1",
	}
	nonPublic := []string{
		"0.0.0.0", "0.255.255.255", "10.0.0.0", "10.255.255.255", "127.0.0.1",
		"127.255.255.255", "169.254.0.0", "169.254.255.255", "172.16.0.0", "172.31.255.255",
		"192.168.0.0", "192.168.255.255", "100.64.0.0", "100.127.255.255", "192.0.0.0",
		"192.0.0.8", "192.0.0.11", "192.0.0.255", "192.0.2.0", "192.0.2.255",
		"192.88.99.0", "192.88.99.255", "198.18.0.0", "198.19.255.255", "198.51.100.0",
		"198.51.100.255", "203.0.113.0", "203.0.113.255", "224.0.0.0", "239.255.255.255",
		"240.0.0.0", "255.255.255.255", "::", "::1", "::8.8.8.8", "64:ff9b::808:808",
		"100::1", "fc00::1", "fdff::1", "fe80::1", "fec0::1", "ff02::1", "2001::1",
		"2001:1ff:ffff:ffff:ffff:ffff:ffff:ffff", "2001:2::1", "2001:20::1", "2001:db8::",
		"2001:db8:ffff::1", "3fff::", "3fff:fff:ffff:ffff:ffff:ffff:ffff:ffff",
		"2002:a00:1::", "2002:808:808::", "4000::1", "::ffff:192.168.1.1",
		"::ffff:c0a8:101", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:172.16.0.1",
		"::ffff:100.64.0.1", "::ffff:0.0.0.0", "::ffff:192.0.2.1", "::ffff:224.0.0.1",
		"https://192.168.1.1:443/", "127.0.0.1?8.8.8.8", "192.168.1.1 8.8.8.8",
		"[::ffff:192.168.1.1]:443/", "https://[::ffff:c0a8:101]:not-a-port",
	}
	for _, in := range public {
		if addr, err := ParseIP(in); err != nil || !IsPublic(addr) {
			t.Errorf("%q: addr %v err %v; want public", in, addr, err)
		}
	}
	for _, in := range nonPublic {
		addr, err := ParseIP(in)
		if err != nil {
			t.Errorf("%q must parse independently of policy: %v", in, err)
		} else if IsPublic(addr) {
			t.Errorf("%q must not be public", in)
		}
	}
}

func TestIsPublicRejectsZeroAndZoned(t *testing.T) {
	if IsPublic(netip.Addr{}) {
		t.Error("zero Addr must not be public")
	}
	if IsPublic(netip.MustParseAddr("2606:4700:4700::1111%eth0")) {
		t.Error("zoned address must not be public")
	}
}

func TestCountryFlag(t *testing.T) {
	for code, want := range map[string]string{
		"US": "\U0001F1FA\U0001F1F8", "cn": "\U0001F1E8\U0001F1F3", "gB": "\U0001F1EC\U0001F1E7",
		"": "", "USA": "", "U1": "", "\u00e9S": "", "--": "",
	} {
		if got := CountryFlag(code); got != want {
			t.Errorf("CountryFlag(%q) = %q; want %q", code, got, want)
		}
	}
}
