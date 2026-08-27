package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/fetch"
	"adassay.com/internal/store"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func corpus(t *testing.T, name string) []byte {
	t.Helper()
	page, err := os.ReadFile(filepath.Join("..", "..", "testdata", "corpus", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return page
}

// A page whose article body is a stub while the chrome around it carries the
// text — what a JavaScript-rendered page looks like to a fetcher without a browser.
func jsRendered() []byte {
	chrome := strings.Repeat("<li><a href=\"/section\">Some navigation entry that is part of the site chrome</a></li>", 40)
	return []byte(`<!DOCTYPE html><html><head><title>Loading</title></head><body>` +
		`<nav><ul>` + chrome + `</ul></nav>` +
		`<article><p>Loading the article, please enable JavaScript.</p></article>` +
		`<footer><ul>` + chrome + `</ul></footer></body></html>`)
}

func TestAnalyze(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		fetch    func(string) ([]byte, error)
		wantCode string
		check    func(*testing.T, core.Result)
	}{
		{
			name:  "an ad-carrying page comes back with verdicts",
			url:   "https://example.com/best-keyboards",
			fetch: func(string) ([]byte, error) { return corpus(t, "promo_listicle.html"), nil },
			check: func(t *testing.T, res core.Result) {
				if len(res.Segments) == 0 {
					t.Fatal("no segments")
				}
				var dropped int
				for _, s := range res.Segments {
					if s.Verdict == core.Drop {
						dropped++
					}
				}
				if dropped == 0 {
					t.Error("nothing was cut on a promo listicle")
				}
			},
		},
		{
			name:  "hidden text is content, not a failure",
			url:   "https://example.com/ssg",
			fetch: func(string) ([]byte, error) { return corpus(t, "inj_hidden_recommend.html"), nil },
			check: func(t *testing.T, res core.Result) {
				if len(res.Hidden) == 0 {
					t.Error("the hidden block was not reported")
				}
			},
		},
		{
			name:     "bot protection",
			url:      "https://example.com/paywalled",
			fetch:    func(string) ([]byte, error) { return nil, fmt.Errorf("adassay: fetch: 403: %w", fetch.ErrBlocked) },
			wantCode: "blocked",
		},
		{
			name:     "a javascript-rendered page comes back thin",
			url:      "https://example.com/app",
			fetch:    func(string) ([]byte, error) { return jsRendered(), nil },
			wantCode: "thin",
			check: func(t *testing.T, res core.Result) {
				if !res.Thin {
					t.Error("thin code without a thin result")
				}
			},
		},
		{
			name:     "malformed url",
			url:      "http://%zz",
			fetch:    func(string) ([]byte, error) { t.Error("fetched a malformed url"); return nil, nil },
			wantCode: "invalid",
		},
		{
			name:     "non-http scheme",
			url:      "file:///etc/passwd",
			fetch:    func(string) ([]byte, error) { t.Error("fetched a file url"); return nil, nil },
			wantCode: "invalid",
		},
		{
			name:     "transport error",
			url:      "https://example.com/gone",
			fetch:    func(string) ([]byte, error) { return nil, errors.New("adassay: fetch: no such host") },
			wantCode: "failed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Analyzer{Cfg: testConfig(t), Fetch: c.fetch}
			res, err := a.Analyze(c.url)
			switch {
			case c.wantCode == "" && err != nil:
				t.Fatalf("Analyze: %v", err)
			case c.wantCode != "" && err == nil:
				t.Fatalf("Analyze succeeded, want code %q", c.wantCode)
			case c.wantCode != "" && Code(err) != c.wantCode:
				t.Fatalf("code = %q (%v), want %q", Code(err), err, c.wantCode)
			}
			if c.check != nil {
				c.check(t, res)
			}
		})
	}
}

func TestCodeOfAnUnlabelledError(t *testing.T) {
	if got := Code(errors.New("boom")); got != "failed" {
		t.Errorf("Code = %q, want %q", got, "failed")
	}
}

func TestAnalyzeOpensNoDatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(store.EnvDB, filepath.Join(dir, "adassay.db"))

	a := &Analyzer{Cfg: testConfig(t), Fetch: func(string) ([]byte, error) { return corpus(t, "promo_listicle.html"), nil }}
	if _, err := a.Analyze("https://example.com/best-keyboards"); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the demo wrote %v, it must open no database", entries)
	}
}
