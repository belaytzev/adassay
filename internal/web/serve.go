package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/render"
)

// maxRequestBody is generous for a JSON object holding one URL.
const maxRequestBody = 4 << 10

// response is core.Result with room for a failure code. render.JSON cannot be
// reused: it encodes core.Result, which has nowhere to put one.
type response struct {
	core.Result
	Error string `json:"error,omitempty"`
}

// status is the HTTP status each code answers with. A thin page still carries
// a result, so it is a 200 with an explanation beside it.
var status = map[string]int{
	"invalid": http.StatusBadRequest,
	"blocked": http.StatusBadGateway,
	"failed":  http.StatusBadGateway,
	"thin":    http.StatusOK,
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
	// "GET /{$}" and not "GET /": a catch-all would swallow a GET to
	// /api/analyze and answer it with the page instead of 405.
	mux.HandleFunc("GET /{$}", handleIndex)
	mux.HandleFunc("POST /api/analyze", func(w http.ResponseWriter, r *http.Request) {
		handleAnalyze(w, r, a)
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

func handleAnalyze(w http.ResponseWriter, r *http.Request, a *Analyzer) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
		write(w, core.Result{}, "invalid")
		return
	}

	res, err := a.Analyze(req.URL)
	if err != nil {
		write(w, res, Code(err))
		return
	}
	write(w, res, "")
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
