package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxResponseBytes = 64 * 1024
	maxJSONDepth     = 64
	// LookupTimeout is the hard upper bound of one Lookup call.
	LookupTimeout = 25 * time.Second

	maxCountryUnits = 96
	maxASNameUnits  = 160

	// UnknownCountryCode is returned when the API gives no valid alpha-2 code.
	UnknownCountryCode = "--"
)

var (
	// ErrMissingToken: the token is empty. No request was made.
	ErrMissingToken = errors.New("missing API token")
	// ErrInvalidToken: the token has characters outside [A-Za-z0-9_-].
	// No request was made.
	ErrInvalidToken = errors.New("invalid API token")
	// ErrNonPublic: the address is not public. No request was made.
	ErrNonPublic = errors.New("non-public IP address: lookup skipped")
	// ErrResponseTooLarge: the response body exceeds 64 KiB.
	ErrResponseTooLarge = errors.New("response exceeds 64 KiB")
	// ErrInvalidUTF8: the response body is empty or not valid UTF-8.
	ErrInvalidUTF8 = errors.New("empty response or invalid UTF-8")
	// ErrInvalidJSON: the body is not one valid JSON object with string/null
	// country_code, country and as_name fields.
	ErrInvalidJSON = errors.New("invalid JSON response")
	// ErrAPIError: the JSON object contains an "error" key.
	ErrAPIError = errors.New("API returned an error")
)

// HTTPError is returned (as *HTTPError) for any non-200 status, including
// 3xx: redirects are never followed.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string { return "HTTP status " + strconv.Itoa(e.StatusCode) }

// NetworkError is a transport failure. Message never contains the request
// URL or token. Context cancellation and the 25s limit are reported as
// context.Canceled / context.DeadlineExceeded instead.
type NetworkError struct {
	Message string
	Timeout bool
}

func (e *NetworkError) Error() string { return "network error: " + e.Message }

// Info is a lookup result. Fields the API did not provide stay empty so the
// caller can localize "unknown". CountryCode is upper-case ASCII alpha-2 or
// "--". Country and ASName are single-line and limited to 96/160 UTF-16 units.
// On errors after the input parsed as an IP, only IP is set.
type Info struct {
	IP          string
	CountryCode string
	Country     string
	ASName      string
}

// Client performs IPinfo Lite lookups over a reused HTTPS connection pool.
// It is safe for concurrent use.
type Client struct {
	http *http.Client
}

// NewClient returns a Client with its own connection pool, honoring the
// environment proxy settings.
func NewClient() *Client {
	return newClient(&http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          2,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	})
}

func newClient(rt http.RoundTripper) *Client {
	return &Client{http: &http.Client{
		Transport: rt,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// Lookup parses input like ParseIP, rejects non-public addresses and invalid
// tokens before any network activity, then queries
// https://api.ipinfo.io/lite/<ip>. Errors: ErrInvalidIP, ErrNonPublic,
// ErrMissingToken, ErrInvalidToken, *HTTPError, *NetworkError, ctx errors,
// ErrResponseTooLarge, ErrInvalidUTF8, ErrInvalidJSON, ErrAPIError.
func (c *Client) Lookup(ctx context.Context, input, token string) (Info, error) {
	addr, err := ParseIP(input)
	if err != nil {
		return Info{}, err
	}
	info := Info{IP: addr.String()}
	if !IsPublic(addr) {
		return info, ErrNonPublic
	}
	if token == "" {
		return info, ErrMissingToken
	}
	for i := range len(token) {
		ch := token[i]
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') &&
			!(ch >= '0' && ch <= '9') && ch != '_' && ch != '-' {
			return info, ErrInvalidToken
		}
	}

	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	u := url.URL{Scheme: "https", Host: "api.ipinfo.io", Path: "/lite/" + info.IP, RawQuery: "token=" + token}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return info, &NetworkError{Message: "cannot build request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ipmon/1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return info, requestError(ctx, err, u.String(), token)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, &HTTPError{StatusCode: resp.StatusCode}
	}
	if resp.ContentLength > maxResponseBytes {
		return info, ErrResponseTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return info, requestError(ctx, err, u.String(), token)
	}
	if len(body) > maxResponseBytes {
		return info, ErrResponseTooLarge
	}
	if len(body) == 0 || !utf8.Valid(body) {
		return info, ErrInvalidUTF8
	}
	fields, err := parseResponse(body)
	if err != nil {
		return info, err
	}
	info.CountryCode = normalizeCountryCode(fields.countryCode)
	info.Country = displayText(fields.country, maxCountryUnits)
	info.ASName = displayText(fields.asName, maxASNameUnits)
	return info, nil
}

// requestError strips the *url.Error wrapper (it carries the full URL, token
// included) and redacts any remaining occurrence of secrets.
func requestError(ctx context.Context, err error, secrets ...string) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	msg := err.Error()
	for _, s := range secrets {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	var t interface{ Timeout() bool }
	return &NetworkError{Message: msg, Timeout: errors.As(err, &t) && t.Timeout()}
}

type responseFields struct{ countryCode, country, asName string }

// parseResponse validates the whole document (including ignored values) and
// extracts the three string/null fields of the root object.
func parseResponse(body []byte) (responseFields, error) {
	var f responseFields
	if !structureOK(body) {
		return f, ErrInvalidJSON
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil || root == nil {
		return f, ErrInvalidJSON
	}
	for _, field := range []struct {
		key string
		dst *string
	}{{"country_code", &f.countryCode}, {"country", &f.country}, {"as_name", &f.asName}} {
		raw, ok := root[field.key]
		if !ok {
			continue
		}
		var s *string
		if err := json.Unmarshal(raw, &s); err != nil {
			return f, fmt.Errorf("%w: %s", ErrInvalidJSON, field.key)
		}
		if s != nil {
			*field.dst = *s
		}
	}
	if _, ok := root["error"]; ok {
		return f, ErrAPIError
	}
	return f, nil
}

// structureOK rejects nesting deeper than maxJSONDepth and unpaired \u
// surrogate escapes anywhere in the document (encoding/json would silently
// replace them with U+FFFD).
func structureOK(b []byte) bool {
	depth, inString := 0, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inString:
			switch c {
			case '"':
				inString = false
			case '\\':
				i++
				if i >= len(b) || b[i] != 'u' {
					continue
				}
				n, ok := hex4(b, i+1)
				if !ok {
					return false
				}
				i += 4
				switch {
				case n >= 0xD800 && n <= 0xDBFF:
					if i+2 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
						return false
					}
					low, ok := hex4(b, i+3)
					if !ok || low < 0xDC00 || low > 0xDFFF {
						return false
					}
					i += 6
				case n >= 0xDC00 && n <= 0xDFFF:
					return false
				}
			}
		case c == '"':
			inString = true
		case c == '{' || c == '[':
			if depth++; depth > maxJSONDepth {
				return false
			}
		case c == '}' || c == ']':
			depth--
		}
	}
	return true
}

func hex4(b []byte, at int) (int, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	n := 0
	for _, c := range b[at : at+4] {
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			c = c - 'A' + 10
		default:
			return 0, false
		}
		n = n<<4 | int(c)
	}
	return n, true
}

func normalizeCountryCode(code string) string {
	if len(code) != 2 {
		return UnknownCountryCode
	}
	var out [2]byte
	for i := range 2 {
		c := code[i] | 0x20
		if c < 'a' || c > 'z' {
			return UnknownCountryCode
		}
		out[i] = c - 32
	}
	return string(out[:])
}

// displayText makes API text safe for a single-line label: control, bidi and
// invisible format characters become spaces, the result is trimmed and cut to
// limit UTF-16 units without splitting a surrogate pair. Empty stays empty.
func displayText(s string, limit int) string {
	s = strings.Trim(strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) ||
			(r >= 0x200b && r <= 0x200f) || (r >= 0x2028 && r <= 0x202e) ||
			(r >= 0x2060 && r <= 0x206f) || r == 0x061c || r == 0xfeff {
			return ' '
		}
		return r
	}, s), " ")
	units := 0
	for i, r := range s {
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		if units+n > limit {
			s = s[:i]
			break
		}
		units += n
	}
	return strings.TrimRight(s, " ")
}

// CountryFlag returns the regional-indicator flag emoji for an ASCII alpha-2
// code (any case), or "" for anything else.
func CountryFlag(code string) string {
	if len(code) != 2 {
		return ""
	}
	var flag [2]rune
	for i := range 2 {
		c := code[i] | 0x20
		if c < 'a' || c > 'z' {
			return ""
		}
		flag[i] = 0x1F1E6 + rune(c-'a')
	}
	return string(flag[:])
}
