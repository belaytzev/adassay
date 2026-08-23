package fetch

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

const (
	Timeout   = 20 * time.Second
	UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	MaxBody   = 8 << 20
)

var ErrBlocked = errors.New("the site refused the request; it is behind bot protection")

var browserHeaders = map[string]string{
	"User-Agent":                UserAgent,
	"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
	"Accept-Language":           "en-US,en;q=0.9",
	"Upgrade-Insecure-Requests": "1",
	"Sec-Fetch-Dest":            "document",
	"Sec-Fetch-Mode":            "navigate",
	"Sec-Fetch-Site":            "none",
	"Sec-Fetch-User":            "?1",
}

func Get(pageURL string) ([]byte, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("adassay: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("adassay: fetch %s: only http and https are fetched", pageURL)
	}
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("adassay: %w", err)
	}
	for name, value := range browserHeaders {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adassay: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if blocked(resp) {
			return nil, fmt.Errorf("adassay: fetch %s: %s: %w", pageURL, resp.Status, ErrBlocked)
		}
		return nil, fmt.Errorf("adassay: fetch %s: %s", pageURL, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("adassay: fetch %s: %w", pageURL, err)
	}
	return page, nil
}

func blocked(resp *http.Response) bool {
	if resp.Header.Get("cf-mitigated") != "" {
		return true
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusUnavailableForLegalReasons:
		return true
	}
	return false
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

var client = &http.Client{
	Timeout: Timeout,
	Transport: &http.Transport{DialContext: (&net.Dialer{
		Timeout: Timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			addr, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			if ip := addr.Addr().Unmap(); ip.IsPrivate() || cgnat.Contains(ip) ||
				ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("adassay: refusing to fetch a private address (%s)", ip)
			}
			return nil
		},
	}).DialContext},
}
