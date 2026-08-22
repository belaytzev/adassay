package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

const (
	// Writes are batched: a client submits its outbox at most a few times a
	// day, so a sustained rate this low still never blocks an honest install.
	writesPerSecond = 1
	writeBurst      = 20
	maxTrackedIPs   = 4096
)

// guard holds everything the write endpoints need to tell clients apart.
type guard struct {
	trusted []netip.Prefix
	lim     *limiter
}

func newGuard(trustedProxies string) (*guard, error) {
	trusted, err := parseTrusted(trustedProxies)
	if err != nil {
		return nil, err
	}
	return &guard{trusted: trusted, lim: newLimiter(writesPerSecond, writeBurst)}, nil
}

func (g *guard) limit(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.lim.allow(clientIP(r, g.trusted)) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "too many writes from this address")
			return
		}
		h(w, r)
	}
}

// clientIP resolves the address a rate limit applies to. Behind a Cloudflare
// Tunnel every request arrives from the same peer, so CF-Connecting-IP is the
// only usable identity — and it counts only when the peer that set it is a
// proxy we run. From anyone else the header is attacker-controlled and a rate
// limit keyed on it limits nothing.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if !isTrusted(peer, trusted) {
		return peer.Unmap().String()
	}
	fwd, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
	if err != nil {
		return peer.Unmap().String()
	}
	return fwd.Unmap().String()
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseTrusted reads a comma-separated list of proxy addresses or CIDRs.
func parseTrusted(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.Split(list, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if p, err := netip.ParsePrefix(field); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("server: trusted proxy %q is neither an address nor a CIDR", field)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// ponytail: in-process limiter, redis if the server ever runs more than one replica
type limiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	seen  map[string]*rate.Limiter
}

func newLimiter(r rate.Limit, burst int) *limiter {
	return &limiter{rate: r, burst: burst, seen: map[string]*rate.Limiter{}}
}

func (l *limiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.seen[ip]
	if !ok {
		if len(l.seen) >= maxTrackedIPs {
			l.prune()
		}
		lim = rate.NewLimiter(l.rate, l.burst)
		l.seen[ip] = lim
	}
	return lim.Allow()
}

// prune drops the entries that carry no state: a full bucket is identical to a
// fresh one, so forgetting it cannot hand anyone extra allowance.
func (l *limiter) prune() {
	for ip, lim := range l.seen {
		if lim.Tokens() >= float64(l.burst) {
			delete(l.seen, ip)
		}
	}
}
