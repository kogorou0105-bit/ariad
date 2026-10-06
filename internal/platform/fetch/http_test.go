package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ariad/internal/ingestion"
)

func TestHTTPFetcherResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		handler   http.Handler
		timeout   time.Duration
		maxBytes  int64
		wantBody  string
		wantError error
	}{
		{
			name: "normal page",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(`<html><body><p>Public page</p></body></html>`))
			}),
			timeout:  time.Second,
			maxBytes: 1_024,
			wantBody: `<html><body><p>Public page</p></body></html>`,
		},
		{
			name: "non success",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusBadGateway)
			}),
			timeout:   time.Second,
			maxBytes:  1_024,
			wantError: ErrHTTPStatus,
		},
		{
			name: "timeout",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				time.Sleep(100 * time.Millisecond)
				_, _ = response.Write([]byte("late"))
			}),
			timeout:   10 * time.Millisecond,
			maxBytes:  1_024,
			wantError: context.DeadlineExceeded,
		},
		{
			name: "too large",
			handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(strings.Repeat("x", 33)))
			}),
			timeout:   time.Second,
			maxBytes:  32,
			wantError: ErrResponseTooLarge,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(test.handler)
			defer server.Close()
			fetcher := newHTTPFetcher(test.timeout, test.maxBytes, true)
			page, err := fetcher.Fetch(context.Background(), server.URL)
			if test.wantError != nil {
				if !errors.Is(err, test.wantError) {
					t.Fatalf("error = %v, want %v", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if string(page.HTML) != test.wantBody {
				t.Fatalf("body = %q, want %q", page.HTML, test.wantBody)
			}
		})
	}
}

func TestHTTPFetcherRejectsUnsafeURLs(t *testing.T) {
	t.Parallel()
	fetcher := NewHTTPFetcher()
	tests := map[string]error{
		"ftp://example.com/file":           ingestion.ErrInvalidURL,
		"http://127.0.0.1/":                ingestion.ErrUnsafeURL,
		"http://10.1.2.3/":                 ingestion.ErrUnsafeURL,
		"http://172.16.1.1/":               ingestion.ErrUnsafeURL,
		"http://192.168.1.1/":              ingestion.ErrUnsafeURL,
		"http://169.254.169.254/latest":    ingestion.ErrUnsafeURL,
		"http://[::1]/":                    ingestion.ErrUnsafeURL,
		"http://0.0.0.0/":                  ingestion.ErrUnsafeURL,
		"http://2130706433/":               ingestion.ErrUnsafeURL,
		"http://0x7f000001/":               ingestion.ErrUnsafeURL,
		"http://0x7f.0x0.0x0.0x1/":         ingestion.ErrUnsafeURL,
		"http://0177.0.0.1/":               ingestion.ErrUnsafeURL,
		"http://127.1/":                    ingestion.ErrUnsafeURL,
		"http://[::ffff:127.0.0.1]/":       ingestion.ErrUnsafeURL,
		"http://metadata.google.internal/": ingestion.ErrUnsafeURL,
	}
	for rawURL, want := range tests {
		rawURL, want := rawURL, want
		t.Run(rawURL, func(t *testing.T) {
			t.Parallel()
			_, err := fetcher.Fetch(context.Background(), rawURL)
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

type staticResolver struct {
	addresses []net.IPAddr
}

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

func TestHTTPFetcherRejectsPrivateDNSResults(t *testing.T) {
	t.Parallel()
	fetcher := NewHTTPFetcher()
	fetcher.resolver = staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}}
	_, err := fetcher.Fetch(context.Background(), "https://public-name.example/page")
	if !errors.Is(err, ingestion.ErrUnsafeURL) {
		t.Fatalf("error = %v, want ErrUnsafeURL", err)
	}
}

func TestHTTPFetcherAllowsOrdinaryHostnameContainingHexCharacters(t *testing.T) {
	t.Parallel()
	fetcher := NewHTTPFetcher()
	fetcher.resolver = staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}}
	addresses, err := fetcher.resolveAllowed(context.Background(), "a1.de")
	if err != nil {
		t.Fatalf("resolve ordinary hostname: %v", err)
	}
	if len(addresses) != 1 || !addresses[0].Equal(net.ParseIP("93.184.216.34")) {
		t.Fatalf("addresses = %#v", addresses)
	}
}

func TestLooksNumericHostnameUsesNumericComponentGrammar(t *testing.T) {
	t.Parallel()
	if looksNumericHostname("1x2") {
		t.Fatal("1x2 must be treated as an ordinary hostname")
	}
	for _, hostname := range []string{"2130706433", "0x7f000001", "0x7f.0.0.1"} {
		if !looksNumericHostname(hostname) {
			t.Fatalf("%q must be treated as numeric", hostname)
		}
	}
}

func TestHTTPFetcherRevalidatesRedirectDestination(t *testing.T) {
	t.Parallel()
	fetcher := NewHTTPFetcher()
	redirectURL, err := url.Parse("http://127.0.0.1/admin")
	if err != nil {
		t.Fatalf("parse redirect URL: %v", err)
	}
	err = fetcher.client.CheckRedirect(&http.Request{URL: redirectURL}, nil)
	if !errors.Is(err, ingestion.ErrUnsafeURL) {
		t.Fatalf("redirect error = %v, want ErrUnsafeURL", err)
	}
}
