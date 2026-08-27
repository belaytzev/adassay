package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/render"
)

// maxRequestBody is generous for a JSON object holding one URL.
const maxRequestBody = 4 << 10

const (
	analyzeRate   = rate.Limit(1)
	analyzeBurst  = 5
	maxTrackedIPs = 4096

	maxCached = 256
	cacheTTL  = 5 * time.Minute
)

// response is core.Result with room for a failure code. render.JSON cannot be
// reused: it encodes core.Result, which has nowhere to put one.
type response struct {
	core.Result
	Error string `json:"error,omitempty"`
}

// status is the HTTP status each code answers with. A thin page still carries
// a result, so it is a 200 with an explanation beside it.
var status = map[string]int{
	"invalid":      http.StatusBadRequest,
	"blocked":      http.StatusBadGateway,
	"failed":       http.StatusBadGateway,
	"thin":         http.StatusOK,
	"rate_limited": http.StatusTooManyRequests,
}

func write(w http.ResponseWriter, res core.Result, code string) {
	res.Text = render.Markdown(res)
	res = render.Safe(res)

	st := http.StatusOK
	if code != "" {
		if s, ok := status[code]; ok {
			st = s
		} else {
			st = http.StatusBadGateway
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(st)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(response{Result: res, Error: code})
}

func newMux(a *Analyzer) *http.ServeMux {
	mux := http.NewServeMux()
	lim, c := newLimiter(), newCache()
	// "GET /{$}" and not "GET /": a catch-all would swallow a GET to
	// /api/analyze and answer it with the page instead of 405.
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("POST /api/analyze", func(w http.ResponseWriter, r *http.Request) {
		handleAnalyze(w, r, a, lim, c)
	})
	mux.HandleFunc("GET /api/case/{slug}", handleCase)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// ponytail: placeholder page, replaced by the embedded site in task 6
func handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "adassay")
}

func handleCase(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func handleAnalyze(w http.ResponseWriter, r *http.Request, a *Analyzer, lim *limiter, c *cache) {
	if !lim.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		write(w, core.Result{}, "rate_limited")
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
		write(w, core.Result{}, "invalid")
		return
	}

	if res, code, ok := c.get(req.URL); ok {
		write(w, res, code)
		return
	}

	res, err := a.Analyze(req.URL)
	code := ""
	if err != nil {
		code = Code(err)
	}
	// Only verdicts are worth keeping: a block or a transport failure is a
	// property of the moment, not of the page.
	if code == "" || code == "thin" {
		c.put(req.URL, res, code)
	}
	write(w, res, code)
}

// ponytail: per-IP table, one global limiter if the table itself becomes the cost
type limiter struct {
	mu   sync.Mutex
	seen map[string]*rate.Limiter
}

func newLimiter() *limiter { return &limiter{seen: map[string]*rate.Limiter{}} }

// allow refuses an unknown address once the table is full rather than clearing
// it: a flood of fresh addresses must not evict the entries holding it back.
func (l *limiter) allow(ip string) bool {
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
		lim = rate.NewLimiter(analyzeRate, analyzeBurst)
		l.seen[ip] = lim
	}
	return lim.Allow()
}

// prune drops the addresses back at a full bucket: forgetting them costs
// nothing, since a new entry starts full too.
func (l *limiter) prune() {
	for ip, lim := range l.seen {
		if lim.Tokens() >= float64(analyzeBurst) {
			delete(l.seen, ip)
		}
	}
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	// One client routinely holds a whole /64, so keying on the full address
	// would leave the limit free to walk around.
	if addr = addr.Unmap(); addr.Is6() {
		return netip.PrefixFrom(addr, 64).Masked().String()
	}
	return addr.String()
}

type entry struct {
	res  core.Result
	code string
	at   time.Time
}

type cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

func newCache() *cache { return &cache{entries: map[string]entry{}} }

func (c *cache) get(url string) (core.Result, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[url]
	if !ok {
		return core.Result{}, "", false
	}
	if time.Since(e.at) > cacheTTL {
		delete(c.entries, url)
		return core.Result{}, "", false
	}
	return e.res, e.code, true
}

func (c *cache) put(url string, res core.Result, code string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[url]; !ok && len(c.entries) >= maxCached {
		c.expire()
		if len(c.entries) >= maxCached {
			return
		}
	}
	c.entries[url] = entry{res: res, code: code, at: time.Now()}
}

func (c *cache) expire() {
	for url, e := range c.entries {
		if time.Since(e.at) > cacheTTL {
			delete(c.entries, url)
		}
	}
}

func Serve(addr string, cfg *config.Config) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(&Analyzer{Cfg: cfg}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Above fetch.Timeout (20s) plus pipeline time, with room to spare.
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "adassay-web on %s\n", addr)
	return srv.ListenAndServe()
}
