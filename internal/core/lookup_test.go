package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testToken = "tok_SECRET-123"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
		// ContentLength -1: the size limit must hold for streamed bodies.
		ContentLength: -1,
	}
}

// lookupWith runs one lookup against a fake transport answering status/body
// and returns the number of requests the transport saw.
func lookupWith(t *testing.T, status int, body string) (Info, error, int) {
	t.Helper()
	calls := 0
	c := newClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return reply(status, body), nil
	}))
	info, err := c.Lookup(context.Background(), "8.8.8.8", testToken)
	return info, err, calls
}

func TestLookupRejectsBeforeAnyRequest(t *testing.T) {
	calls := 0
	c := newClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return reply(200, `{}`), nil
	}))
	cases := []struct {
		name, input, token string
		want               error
		wantIP             string
	}{
		{"garbage", "not-an-ip", testToken, ErrInvalidIP, ""},
		{"private", "https://192.168.1.1/x", testToken, ErrNonPublic, "192.168.1.1"},
		{"mapped private", "::ffff:10.0.0.1", testToken, ErrNonPublic, "::ffff:10.0.0.1"},
		{"documentation v6", "2001:db8::1", testToken, ErrNonPublic, "2001:db8::1"},
		{"missing token", "8.8.8.8", "", ErrMissingToken, "8.8.8.8"},
		{"query injection", "8.8.8.8", "a&b=c", ErrInvalidToken, "8.8.8.8"},
		{"path injection", "8.8.8.8", "a/../b", ErrInvalidToken, "8.8.8.8"},
		{"space", "8.8.8.8", "a b", ErrInvalidToken, "8.8.8.8"},
		{"NUL", "8.8.8.8", "a\x00b", ErrInvalidToken, "8.8.8.8"},
		{"non-ASCII", "8.8.8.8", "tokén", ErrInvalidToken, "8.8.8.8"},
		{"newline", "8.8.8.8", "abc\r\nX: y", ErrInvalidToken, "8.8.8.8"},
	}
	for _, tc := range cases {
		info, err := c.Lookup(context.Background(), tc.input, tc.token)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v; want %v", tc.name, err, tc.want)
		}
		if info.IP != tc.wantIP || info.CountryCode != "" || info.Country != "" || info.ASName != "" {
			t.Errorf("%s: info = %+v; want only IP %q", tc.name, info, tc.wantIP)
		}
	}
	if calls != 0 {
		t.Fatalf("transport called %d times for rejected input", calls)
	}
}

func TestLookupRequestShapeAndSuccess(t *testing.T) {
	var got *http.Request
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r
		return reply(200, `{"ip":"8.8.8.8","asn":"AS15169","as_name":"Google LLC","as_domain":"google.com",`+
			`"country_code":"us","country":"United States","continent":"North America","extra":{"a":[1,2.5e3,true,null,"x"]}}`), nil
	}))
	info, err := c.Lookup(context.Background(), " http://8.8.8.8:80/ignored", testToken)
	if err != nil {
		t.Fatal(err)
	}
	if info != (Info{IP: "8.8.8.8", CountryCode: "US", Country: "United States", ASName: "Google LLC"}) {
		t.Errorf("info = %+v", info)
	}
	if got.Method != http.MethodGet || got.URL.Scheme != "https" || got.URL.Host != "api.ipinfo.io" ||
		got.URL.Path != "/lite/8.8.8.8" || got.URL.RawQuery != "token="+testToken {
		t.Errorf("request = %s %s", got.Method, got.URL.Redacted())
	}
	if got.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q", got.Header.Get("Accept"))
	}
	dl, ok := got.Context().Deadline()
	if !ok || time.Until(dl) > LookupTimeout || time.Until(dl) < LookupTimeout-5*time.Second {
		t.Errorf("request deadline %v (ok=%v), want about %v", time.Until(dl), ok, LookupTimeout)
	}
}

func TestLookupIPv6PathIsCanonical(t *testing.T) {
	var path string
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		path = r.URL.EscapedPath()
		return reply(200, `{}`), nil
	}))
	if _, err := c.Lookup(context.Background(), "[2606:4700:4700:0:0:0:0:1111]:443", testToken); err != nil {
		t.Fatal(err)
	}
	if path != "/lite/2606:4700:4700::1111" {
		t.Errorf("path = %q", path)
	}
}

func TestLookupNormalizesFields(t *testing.T) {
	long := strings.Repeat("a", 100)
	emoji := strings.Repeat("a", 95) + "😀" // surrogate pair would straddle unit 96
	cases := []struct {
		name, body string
		want       Info
	}{
		{"missing and null fields", `{"country_code":null,"country":null}`,
			Info{IP: "8.8.8.8", CountryCode: "--"}},
		{"bad codes", `{"country_code":"USA"}`, Info{IP: "8.8.8.8", CountryCode: "--"}},
		{"digit code", `{"country_code":"U1"}`, Info{IP: "8.8.8.8", CountryCode: "--"}},
		{"non-ASCII code", `{"country_code":"\u00e9S"}`, Info{IP: "8.8.8.8", CountryCode: "--"}},
		{"mixed case", `{"country_code":"gB"}`, Info{IP: "8.8.8.8", CountryCode: "GB"}},
		{"single line", `{"country":"  A\nB\r\tC\u2028D\u202eE\u200bF\u0085G  ","as_name":"\n\u200b "}`,
			Info{IP: "8.8.8.8", CountryCode: "--", Country: "A B  C D E F G"}},
		{"country limit 96", `{"country":"` + long + `"}`,
			Info{IP: "8.8.8.8", CountryCode: "--", Country: long[:96]}},
		{"as_name limit 160", `{"as_name":"` + strings.Repeat("b", 200) + `"}`,
			Info{IP: "8.8.8.8", CountryCode: "--", ASName: strings.Repeat("b", 160)}},
		{"no split surrogate", `{"country":"` + emoji + `"}`,
			Info{IP: "8.8.8.8", CountryCode: "--", Country: strings.Repeat("a", 95)}},
		{"surrogate pair fits", `{"country":"` + strings.Repeat("a", 94) + `\ud83d\ude00"}`,
			Info{IP: "8.8.8.8", CountryCode: "--", Country: strings.Repeat("a", 94) + "😀"}},
		{"trailing space after cut", `{"country":"` + strings.Repeat("a", 90) + strings.Repeat(" ", 10) + `x"}`,
			Info{IP: "8.8.8.8", CountryCode: "--", Country: strings.Repeat("a", 90)}},
	}
	for _, tc := range cases {
		info, err, _ := lookupWith(t, 200, tc.body)
		if err != nil || info != tc.want {
			t.Errorf("%s: info = %+v, err = %v; want %+v", tc.name, info, err, tc.want)
		}
	}
}

func TestLookupHTTPStatus(t *testing.T) {
	for _, status := range []int{301, 302, 307, 401, 403, 404, 429, 500} {
		info, err, calls := lookupWith(t, status, `{"country_code":"US"}`)
		var he *HTTPError
		if !errors.As(err, &he) || he.StatusCode != status {
			t.Errorf("status %d: err = %v", status, err)
		}
		if info.CountryCode != "" || calls != 1 {
			t.Errorf("status %d: info = %+v, calls = %d", status, info, calls)
		}
	}
}

func TestLookupNeverFollowsRedirect(t *testing.T) {
	var hosts []string
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		resp := reply(302, "")
		resp.Header.Set("Location", "https://evil.example/steal")
		resp.Request = r
		return resp, nil
	}))
	_, err := c.Lookup(context.Background(), "8.8.8.8", testToken)
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != 302 {
		t.Fatalf("err = %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "api.ipinfo.io" {
		t.Fatalf("requested hosts %v", hosts)
	}
}

func TestLookupResponseSizeLimit(t *testing.T) {
	pad := func(n int) string {
		const doc = `{"country_code":"JP"}`
		return doc + strings.Repeat(" ", n-len(doc))
	}
	if info, err, _ := lookupWith(t, 200, pad(64*1024)); err != nil || info.CountryCode != "JP" {
		t.Errorf("exactly 64 KiB: %+v, %v", info, err)
	}
	if _, err, _ := lookupWith(t, 200, pad(64*1024+1)); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("64 KiB + 1: err = %v", err)
	}
	// A declared length alone must not require reading the body.
	c := newClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := reply(200, `{}`)
		r.ContentLength = 1 << 30
		return r, nil
	}))
	if _, err := c.Lookup(context.Background(), "8.8.8.8", testToken); !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("declared oversize: err = %v", err)
	}
}

func TestLookupMalformedBodies(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	cases := []struct {
		name, body string
		want       error
	}{
		{"empty", "", ErrInvalidUTF8},
		{"invalid utf8", "{\"country\":\"\xff\"}", ErrInvalidUTF8},
		{"truncated utf8", "{\"country\":\"\xe4\xb8\"}", ErrInvalidUTF8},
		{"not json", "<html>", ErrInvalidJSON},
		{"array root", `[]`, ErrInvalidJSON},
		{"null root", `null`, ErrInvalidJSON},
		{"trailing data", `{"country":"x"} {}`, ErrInvalidJSON},
		{"truncated", `{"country":"x"`, ErrInvalidJSON},
		{"bad ignored value", `{"x":01,"country":"x"}`, ErrInvalidJSON},
		{"raw control char", "{\"country\":\"a\nb\"}", ErrInvalidJSON},
		{"number country", `{"country":5}`, ErrInvalidJSON},
		{"object as_name", `{"as_name":{}}`, ErrInvalidJSON},
		{"number code", `{"country_code":12}`, ErrInvalidJSON},
		{"lone high surrogate", `{"country":"\ud800"}`, ErrInvalidJSON},
		{"lone low surrogate", `{"x":"\udc00"}`, ErrInvalidJSON},
		{"bad escape", `{"x":"\q"}`, ErrInvalidJSON},
		{"depth 65", `{"x":` + deep(64) + `}`, ErrInvalidJSON},
		{"api error", `{"error":{"title":"Unauthorized"}}`, ErrAPIError},
		{"api error null", `{"error":null,"country_code":"US"}`, ErrAPIError},
	}
	for _, tc := range cases {
		info, err, _ := lookupWith(t, 200, tc.body)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v; want %v", tc.name, err, tc.want)
		}
		if info.CountryCode != "" || info.Country != "" || info.ASName != "" {
			t.Errorf("%s: partial data leaked: %+v", tc.name, info)
		}
	}
	// A nested "error" key is not an API error; a valid surrogate escape pair is accepted.
	if _, err, _ := lookupWith(t, 200, `{"x":{"error":1},"country":"\ud83d\ude00","y":`+deep(62)+`}`); err != nil {
		t.Errorf("valid nested document: %v", err)
	}
}

func TestLookupNetworkErrorHidesTokenAndURL(t *testing.T) {
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: lookup api.ipinfo.io failed for " + r.URL.String() + " using " + testToken)
	}))
	_, err := c.Lookup(context.Background(), "8.8.8.8", testToken)
	var ne *NetworkError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %T %v", err, err)
	}
	for _, secret := range []string{testToken, "token=", "/lite/", "https://"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %q", err, secret)
		}
	}
}

func TestLookupBodyReadErrorHidesToken(t *testing.T) {
	c := newClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		r := reply(200, "")
		r.Body = io.NopCloser(errReader{errors.New("reset while reading " + testToken)})
		return r, nil
	}))
	_, err := c.Lookup(context.Background(), "8.8.8.8", testToken)
	var ne *NetworkError
	if !errors.As(err, &ne) || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestLookupContextCancellation(t *testing.T) {
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Lookup(ctx, "8.8.8.8", testToken)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v; want context.Canceled", err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Error("cancellation error leaks token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lookup did not return after cancel")
	}

	expired, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	if _, err := c.Lookup(expired, "8.8.8.8", testToken); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v; want context.DeadlineExceeded", err)
	}
}
