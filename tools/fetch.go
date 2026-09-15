// SPDX-FileCopyrightText: 2026 Leelsey
// SPDX-License-Identifier: MIT

package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/leelsey/golm"
)

const (
	maxFetchBytes    = 512 << 10
	maxRedirects     = 5
	defaultFetchTime = 30 * time.Second
)

// Fetcher retrieves a URL for the model.
type Fetcher struct {
	AllowPrivate bool

	Timeout time.Duration

	MaxBytes int64

	UserAgent string

	once   sync.Once
	client *http.Client
}

var specialPurpose = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:20::/28"),
}

func v4Embedded(a netip.Addr) (netip.Addr, bool) {
	if a.Is4In6() {
		return a.Unmap(), true
	}
	if !a.Is6() {
		return netip.Addr{}, false
	}
	b := a.As16()

	nat64 := netip.MustParsePrefix("64:ff9b::/96")
	if nat64.Contains(a) {
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	}

	if b[0] == 0x20 && b[1] == 0x02 {
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

func blockedIP(ip net.IP, allowPrivate bool) string {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "unparseable address"
	}
	if why := checkAddr(a.Unmap(), allowPrivate); why != "" {
		return why
	}

	if inner, ok := v4Embedded(a); ok {
		if why := checkAddr(inner, allowPrivate); why != "" {
			return why + " behind a transitional IPv6 address"
		}
	}
	return ""
}

func checkAddr(a netip.Addr, allowPrivate bool) string {
	switch {
	case !a.IsValid():
		return "unparseable address"
	case a.IsUnspecified():
		return "unspecified address"
	case a.IsMulticast(), a.IsInterfaceLocalMulticast(), a.IsLinkLocalMulticast():
		return "multicast address"
	}

	for _, p := range specialPurpose {
		if p.Contains(a) {
			return "reserved address (" + p.String() + ")"
		}
	}
	if allowPrivate {
		return ""
	}
	switch {
	case a.IsLoopback():
		return "loopback address"
	case a.IsLinkLocalUnicast():

		return "link-local address"
	case a.IsPrivate():
		return "private address"
	}
	return ""
}

func (f *Fetcher) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	var lastErr error
	for _, ia := range ips {
		if why := blockedIP(ia.IP, f.AllowPrivate); why != "" {
			lastErr = fmt.Errorf("refusing to connect to %s (%s): %s", host, ia.IP, why)
			continue
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ia.IP.String(), port))
		if err != nil {
			lastErr = err
			continue
		}
		return c, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable address for %s", host)
	}
	return nil, lastErr
}

func (f *Fetcher) httpClient() *http.Client {
	f.once.Do(func() {
		tr := &http.Transport{
			DialContext:           f.dial,
			MaxIdleConnsPerHost:   4,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
		}
		f.client = &http.Client{
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("stopped after %d redirects", maxRedirects)
				}

				return checkURL(req.URL)
			},
		}
	})
	return f.client
}

func checkURL(u *url.URL) error {
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("only http and https are allowed, not %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("no host in %s", u)
	}
	return nil
}

type fetchArgs struct {
	URL string `json:"url" jsonschema:"the http or https URL to retrieve"`
}

// Tool returns the fetch tool.
func (f *Fetcher) Tool() golm.Tool {
	t := golm.NewTypedTool("fetch",
		"Retrieve a web page or API response over http or https and return it as text.",
		func(ctx context.Context, in fetchArgs) (string, error) {
			raw := strings.TrimSpace(in.URL)
			u, err := url.Parse(raw)
			if err != nil {
				return "", fmt.Errorf("invalid URL: %w", err)
			}
			if err := checkURL(u); err != nil {
				return "", err
			}
			timeout := f.Timeout
			if timeout <= 0 {
				timeout = defaultFetchTime
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
			if err != nil {
				return "", err
			}
			ua := f.UserAgent
			if ua == "" {
				ua = "golm/" + golm.Version
			}
			req.Header.Set("User-Agent", ua)
			req.Header.Set("Accept", "text/*, application/json;q=0.9, */*;q=0.1")

			resp, err := f.httpClient().Do(req)
			if err != nil {
				return "", err
			}
			defer func() {
				io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
				resp.Body.Close()
			}()

			limit := f.MaxBytes
			if limit <= 0 {
				limit = maxFetchBytes
			}
			b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
			if err != nil {
				return "", err
			}
			truncated := int64(len(b)) > limit
			if truncated {
				b = b[:limit]
			}
			if !isText(b) {
				return "", fmt.Errorf("%s returned %s, which is not text", u, resp.Header.Get("Content-Type"))
			}
			var sb strings.Builder
			if resp.StatusCode >= 400 {
				fmt.Fprintf(&sb, "[HTTP %d]\n", resp.StatusCode)
			}
			sb.Write(b)
			if truncated {
				fmt.Fprintf(&sb, "\n\n[truncated at %d bytes]", limit)
			}
			return sb.String(), nil
		})
	return golm.WithTraits(t, golm.ToolTraits{ReadOnly: true, Network: true})
}
