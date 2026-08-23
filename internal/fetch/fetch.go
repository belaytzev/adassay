// Package fetch retrieves a page over HTTP for both binaries, so the timeout,
// the body cap and the User-Agent are decided in one place.
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
	UserAgent = "adfilter/0.1 (+https://github.com/belaytzev/adfilter)"
	MaxBody   = 8 << 20
)

func Get(pageURL string) ([]byte, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("adfilter: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("adfilter: fetch %s: only http and https are fetched", pageURL)
	}
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("adfilter: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adfilter: fetch %s: %s", pageURL, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch %s: %w", pageURL, err)
	}
	return page, nil
}

// client refuses to connect to private and link-local addresses. The url can
// come from an agent that is itself acting on a page it just read, so "fetch
// this" must not become a probe of the network the machine sits on — the cloud
// metadata service at 169.254.169.254 above all. The check sits in the dialer
// because that is the only place a redirect passes through too.
// ponytail: loopback stays reachable so a local server can be filtered, tighten it if the agent gets its own network namespace
//
// IsPrivate misses 100.64.0.0/10, which is where a Tailscale or a CGNAT host
// answers — as internal as RFC 1918 and just as much a place not to probe.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// One client for the process: a Transport per call gives up keep-alive and
// leaves its own idle pool behind on every fetch of the long-lived MCP server.
// Control is stateless and still runs on each connection, redirects included.
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
				return fmt.Errorf("adfilter: refusing to fetch a private address (%s)", ip)
			}
			return nil
		},
	}).DialContext},
}
