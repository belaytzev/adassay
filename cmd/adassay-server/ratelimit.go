package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	writesPerSecond = 1
	writeBurst      = maxBatch
	maxTrackedIPs   = 4096
)

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
		return limitKey(peer)
	}
	fwd, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
	if err != nil {
		return limitKey(peer)
	}
	return limitKey(fwd)
}

func limitKey(addr netip.Addr) string {
	addr = addr.Unmap()
	if addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
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

type limiter struct {
	mu    sync.Mutex
	rate  rate.Limit
	burst int
	seen  map[string]*rate.Limiter
}

func newLimiter(r rate.Limit, burst int) *limiter {
	return &limiter{rate: r, burst: burst, seen: map[string]*rate.Limiter{}}
}

func (l *limiter) allow(ip string) bool { return l.allowN(ip, 1) }

func (l *limiter) allowN(ip string, n int) bool {
	if n <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	lim, ok := l.seen[ip]
	if !ok {
		if len(l.seen) >= maxTrackedIPs {
			l.prune()

			if len(l.seen) >= maxTrackedIPs {
				return false
			}
		}
		lim = rate.NewLimiter(l.rate, l.burst)
		l.seen[ip] = lim
	}
	return lim.AllowN(time.Now(), n)
}

func (l *limiter) prune() {
	for ip, lim := range l.seen {
		if lim.Tokens() >= float64(l.burst) {
			delete(l.seen, ip)
		}
	}
}
