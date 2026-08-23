package fetch

import (
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
	UserAgent = "adassay/0.1 (+https://adassay.com)"
	MaxBody   = 8 << 20
)

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
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adassay: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adassay: fetch %s: %s", pageURL, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("adassay: fetch %s: %w", pageURL, err)
	}
	return page, nil
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
