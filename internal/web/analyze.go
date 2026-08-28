// Package web serves the adassay demo: a page in, a proof sheet out.
package web

import (
	"errors"
	"fmt"
	"net/url"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/fetch"
	"adassay.com/internal/pipeline"
)

type Analyzer struct {
	Cfg   *config.Config
	Fetch func(string) ([]byte, error)
}

// Error carries the code the frontend turns into a sentence.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "failed"
}

func (a *Analyzer) Analyze(pageURL string) (core.Result, error) {
	u, err := url.Parse(pageURL)
	// Hostname rather than Host: "http://:80/" parses with a Host of ":80" and
	// would reach the transport, which answers with a failure, not an address.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return core.Result{}, &Error{Code: "invalid", Err: fmt.Errorf("adassay: %q is not an http or https url", pageURL)}
	}
	// A hosted demo that dials any port is a port scanner wearing its address.
	if p := u.Port(); p != "" && p != "80" && p != "443" {
		return core.Result{}, &Error{Code: "invalid", Err: fmt.Errorf("adassay: %q: only the default http and https ports are fetched", pageURL)}
	}

	get := a.Fetch
	if get == nil {
		get = fetch.GetPublic
	}
	page, err := get(pageURL)
	if err != nil {
		if errors.Is(err, fetch.ErrBlocked) {
			return core.Result{}, &Error{Code: "blocked", Err: err}
		}
		return core.Result{}, &Error{Code: "failed", Err: err}
	}

	res, err := extract.Extract(page, pageURL, a.Cfg.L1)
	if err != nil {
		return core.Result{}, &Error{Code: "failed", Err: err}
	}

	// No Cache, no Shared, no Judge: the demo opens no database file and tells
	// the shared server nothing.
	p := &pipeline.Pipeline{Cfg: a.Cfg}
	res, err = p.Run(res)
	if err != nil {
		return core.Result{}, &Error{Code: "failed", Err: err}
	}
	// Hidden findings are content, not a failure, so they travel in the body.
	if res.Thin {
		return res, &Error{Code: "thin", Err: fmt.Errorf("adassay: extraction looks incomplete: %d segment(s), %d of %d visible characters kept", len(res.Segments), len([]rune(res.Text)), res.Visible)}
	}
	return res, nil
}
