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
	// A token is one verdict, not one request. Writes are batched: a client
	// submits its outbox at most a few times a day, so a burst that swallows a
	// whole batch never blocks an honest install, and the sustained rate still
	// caps what a flood can claim.
	writesPerSecond = 1
	writeBurst      = maxBatch
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
		return limitKey(peer)
	}
	fwd, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
	if err != nil {
		return limitKey(peer)
	}
	return limitKey(fwd)
}

// limitKey is the unit a write limit applies to. An IPv4 host is one address,
// but the smallest IPv6 allocation a home connection gets is a /64 — keying on
// the /128 would let one subscriber walk 2^64 fresh token buckets, and walking
// past maxTrackedIPs also clears the buckets of everyone else.
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
			// Every tracked bucket is still spending: the map is full of live
			// state and pruning freed nothing. Forgetting all of it hands back
			// one burst each, which is cheaper than growing without a bound
			// while a flood of fresh addresses turns every write into a scan.
			if len(l.seen) >= maxTrackedIPs {
				clear(l.seen)
			}
		}
		lim = rate.NewLimiter(l.rate, l.burst)
		l.seen[ip] = lim
	}
	return lim.AllowN(time.Now(), n)
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
