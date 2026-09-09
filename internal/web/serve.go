package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/time/rate"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/render"
)

//go:embed site/index.html site/adassay.css site/adassay.js site/logo.png site/logo-dark.png site/fonts
var siteFS embed.FS

var siteRoot, _ = fs.Sub(siteFS, "site")

// assets are listed rather than served under a catch-all: "GET /" would match
// a GET to /api/analyze too, answering 404 where the mux owes a 405.
var assets = []string{
	"adassay.css",
	"adassay.js",
	"logo.png",
	"logo-dark.png",
	"fonts/source-serif-4-latin-cyrillic.woff2",
	"fonts/jetbrains-mono-latin-cyrillic.woff2",
	// OFL-1.1 asks that the licence travel with the fonts it covers.
	"fonts/LICENSE",
}

// maxRequestBody is generous for a JSON object holding one URL.
const maxRequestBody = 4 << 10

const (
	analyzeRate   = rate.Limit(1)
	analyzeBurst  = 5
	maxTrackedIPs = 4096

	maxCached = 256
	cacheTTL  = 5 * time.Minute
	// A result carries the page's whole visible text, and the cache holds it for
	// cacheTTL. Counting entries alone bounds nothing: maxCached results grown
	// from pages of fetch.MaxBody would retain gigabytes long after the
	// in-flight limit below has let them go. A page past this size is simply not
	// cached — re-analysing it costs a slot, which is the bound that holds.
	maxEntryBytes = 256 << 10
)

// maxInFlight bounds concurrent analyses. The rate limiter counts requests per
// address, which does not bound them across addresses, and one analysis holds a
// page of up to fetch.MaxBody plus the two DOMs extract parses from it — so an
// unbounded burst is an out-of-memory kill rather than a slowdown.
const maxInFlight = 4

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
	"busy":         http.StatusServiceUnavailable,
}

func write(w http.ResponseWriter, res core.Result, code string) {
	res.Text = render.Markdown(res)
	res = render.Safe(res)

	st := http.StatusOK
	if s, ok := status[code]; ok {
		st = s
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(st)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(response{Result: res, Error: code})
}

// goGet answers the meta-tag page for every path the go command probes.
// Resolving adassay.com/cmd/adassay walks that path and its prefixes, and each
// probe is a plain GET the mux would 404 before the go-import tag is read.
func goGet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("go-get") == "1" && (r.Method == "GET" || r.Method == "HEAD") {
			http.ServeFileFS(w, r, siteRoot, "index.html")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newMux(a *Analyzer) http.Handler {
	mux := http.NewServeMux()
	lim, c, cases := newLimiter(), newCache(), newCases(a)
	sem := make(chan struct{}, maxInFlight)
	static := http.FileServerFS(siteRoot)
	mux.Handle("GET /{$}", static)
	for _, name := range assets {
		mux.Handle("GET /"+name, static)
	}
	mux.HandleFunc("POST /api/analyze", func(w http.ResponseWriter, r *http.Request) {
		handleAnalyze(w, r, a, lim, c, sem)
	})
	mux.HandleFunc("GET /api/case/{slug}", func(w http.ResponseWriter, r *http.Request) {
		handleCase(w, r, cases)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return goGet(mux)
}

func handleAnalyze(w http.ResponseWriter, r *http.Request, a *Analyzer, lim *limiter, c *cache, sem chan struct{}) {
	if !lim.allow(clientIP(r)) {
		// One token returns a second later; asking for a minute would be a lie.
		w.Header().Set("Retry-After", "1")
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

	// Taken after the cache lookup: a hit only re-renders an entry bounded by
	// maxEntryBytes, which is not what the slot is guarding against.
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		w.Header().Set("Retry-After", "1")
		write(w, core.Result{}, "busy")
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
	if size(res) > maxEntryBytes {
		return
	}

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

// size counts what a cached result keeps alive. The strings are not the whole
// of it: a page of a hundred thousand one-word paragraphs is a few hundred
// kilobytes of text holding tens of megabytes of backing array, so every
// element pays its struct's width whatever it carries.
func size(res core.Result) int {
	n := len(res.Text) + len(res.Title)
	for _, s := range res.Segments {
		n += int(unsafe.Sizeof(s)) + len(s.ID) + len(s.Text)
		for _, r := range s.Reasons {
			n += int(unsafe.Sizeof(r)) + len(r)
		}
		for _, l := range s.Links {
			n += int(unsafe.Sizeof(l)) + len(l.Href) + len(l.Rel) + len(l.Text)
		}
	}
	for _, f := range res.Hidden {
		n += int(unsafe.Sizeof(f)) + len(f.Kind) + len(f.Sample)
	}
	return n
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
