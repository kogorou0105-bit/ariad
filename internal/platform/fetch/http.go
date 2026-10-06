// Package fetch implements bounded public-web fetching for ingestion.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ariad/internal/ingestion"
)

const (
	defaultTimeout          = 15 * time.Second
	defaultMaximumBodyBytes = int64(2 << 20)
	maximumRedirects        = 5
)

var (
	// ErrHTTPStatus indicates that the remote server returned a non-2xx response.
	ErrHTTPStatus = errors.New("remote server returned a non-success status")
	// ErrResponseTooLarge indicates that the decoded response exceeded the configured bound.
	ErrResponseTooLarge = errors.New("remote response exceeds size limit")
)

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// HTTPFetcher retrieves public HTTP(S) pages with SSRF, time and size bounds.
type HTTPFetcher struct {
	client              *http.Client
	resolver            ipResolver
	timeout             time.Duration
	maximumBodyBytes    int64
	allowPrivateForTest bool
}

var _ ingestion.Fetcher = (*HTTPFetcher)(nil)

// NewHTTPFetcher creates the production URL fetch adapter.
func NewHTTPFetcher() *HTTPFetcher {
	return newHTTPFetcher(defaultTimeout, defaultMaximumBodyBytes, false)
}

func newHTTPFetcher(timeout time.Duration, maximumBodyBytes int64, allowPrivateForTest bool) *HTTPFetcher {
	fetcher := &HTTPFetcher{
		resolver:            net.DefaultResolver,
		timeout:             timeout,
		maximumBodyBytes:    maximumBodyBytes,
		allowPrivateForTest: allowPrivateForTest,
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           fetcher.dialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	fetcher.client = &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maximumRedirects {
				return errors.New("too many redirects")
			}
			return fetcher.validateDestination(request.Context(), request.URL)
		},
	}
	return fetcher
}

// Fetch retrieves one HTML page. The synchronous timeout is intentionally
// bounded; a later worker-backed ingestion flow can reuse this adapter.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) (ingestion.FetchedPage, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ingestion.FetchedPage{}, fmt.Errorf("parse URL: %w", ingestion.ErrInvalidURL)
	}
	if err := f.validateDestination(ctx, parsed); err != nil {
		return ingestion.FetchedPage{}, err
	}
	fetchContext, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(fetchContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return ingestion.FetchedPage{}, fmt.Errorf("create fetch request: %w", err)
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "AriadFetcher/1.0")
	response, err := f.client.Do(request)
	if err != nil {
		if fetchContext.Err() != nil {
			return ingestion.FetchedPage{}, fmt.Errorf("fetch timeout: %w", fetchContext.Err())
		}
		return ingestion.FetchedPage{}, fmt.Errorf("fetch URL: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ingestion.FetchedPage{}, fmt.Errorf("%w: HTTP %d", ErrHTTPStatus, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, f.maximumBodyBytes+1))
	if err != nil {
		return ingestion.FetchedPage{}, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > f.maximumBodyBytes {
		return ingestion.FetchedPage{}, ErrResponseTooLarge
	}
	return ingestion.FetchedPage{URL: response.Request.URL.String(), HTML: body}, nil
}

func (f *HTTPFetcher) validateDestination(ctx context.Context, target *url.URL) error {
	if target == nil || target.Hostname() == "" {
		return ingestion.ErrInvalidURL
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		return ingestion.ErrInvalidURL
	}
	if target.User != nil || strings.Contains(target.Hostname(), "%") {
		return ingestion.ErrUnsafeURL
	}
	if port := target.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return ingestion.ErrInvalidURL
		}
	}
	_, err := f.resolveAllowed(ctx, target.Hostname())
	return err
}

func (f *HTTPFetcher) dialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split fetch address: %w", err)
	}
	addresses, err := f.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: f.timeout}
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

func (f *HTTPFetcher) resolveAllowed(ctx context.Context, hostname string) ([]net.IP, error) {
	host := strings.ToLower(strings.TrimSuffix(hostname, "."))
	if isMetadataHostname(host) {
		return nil, ingestion.ErrUnsafeURL
	}
	if parsed := net.ParseIP(host); parsed != nil {
		if !f.allowPrivateForTest && isForbiddenIP(parsed) {
			return nil, ingestion.ErrUnsafeURL
		}
		return []net.IP{parsed}, nil
	}
	if parsed, ok := parseNumericIPv4(host); ok {
		if !f.allowPrivateForTest && isForbiddenIP(parsed) {
			return nil, ingestion.ErrUnsafeURL
		}
		return []net.IP{parsed}, nil
	}
	if looksNumericHostname(host) {
		return nil, ingestion.ErrUnsafeURL
	}
	resolved, err := f.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve URL host: %w", err)
	}
	if len(resolved) == 0 {
		return nil, errors.New("URL host has no IP addresses")
	}
	addresses := make([]net.IP, 0, len(resolved))
	for _, address := range resolved {
		if !f.allowPrivateForTest && isForbiddenIP(address.IP) {
			return nil, ingestion.ErrUnsafeURL
		}
		addresses = append(addresses, address.IP)
	}
	return addresses, nil
}

func isMetadataHostname(host string) bool {
	switch host {
	case "metadata", "metadata.google.internal", "metadata.google", "instance-data":
		return true
	default:
		return (strings.HasPrefix(host, "metadata.") || strings.HasPrefix(host, "instance-data.")) &&
			strings.HasSuffix(host, ".internal")
	}
}

func isForbiddenIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		!ip.IsGlobalUnicast() {
		return true
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	carrierGradeNAT := netip.MustParsePrefix("100.64.0.0/10")
	return carrierGradeNAT.Contains(address)
}

func parseNumericIPv4(host string) (net.IP, bool) {
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return nil, false
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		value, err := parseNumericPart(part)
		if err != nil {
			return nil, false
		}
		values[index] = value
	}
	var combined uint64
	switch len(values) {
	case 1:
		combined = values[0]
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return nil, false
		}
		combined = values[0]<<24 | values[1]
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return nil, false
		}
		combined = values[0]<<24 | values[1]<<16 | values[2]
	case 4:
		for _, value := range values {
			if value > 0xff {
				return nil, false
			}
		}
		combined = values[0]<<24 | values[1]<<16 | values[2]<<8 | values[3]
	}
	if combined > 0xffffffff {
		return nil, false
	}
	return net.IPv4(byte(combined>>24), byte(combined>>16), byte(combined>>8), byte(combined)), true
}

func parseNumericPart(part string) (uint64, error) {
	if part == "" {
		return 0, errors.New("empty numeric address part")
	}
	base := 10
	digits := part
	if strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X") {
		base = 16
		digits = part[2:]
	} else if len(part) > 1 && part[0] == '0' {
		base = 8
		digits = part[1:]
	}
	if digits == "" {
		digits = "0"
	}
	return strconv.ParseUint(digits, base, 32)
}

func looksNumericHostname(host string) bool {
	if host == "" || host[0] < '0' || host[0] > '9' {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" {
			return true
		}
		if strings.HasPrefix(part, "0x") {
			if len(part) == 2 || !containsOnly(part[2:], isHexDigit) {
				return false
			}
			continue
		}
		if !containsOnly(part, isDecimalDigit) {
			return false
		}
	}
	return true
}

func containsOnly(value string, allowed func(byte) bool) bool {
	for index := range value {
		if !allowed(value[index]) {
			return false
		}
	}
	return true
}

func isDecimalDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isHexDigit(character byte) bool {
	return isDecimalDigit(character) || character >= 'a' && character <= 'f'
}
