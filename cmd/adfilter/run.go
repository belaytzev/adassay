package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/extract"
	"github.com/belaytzev/adfilter/internal/judge"
	"github.com/belaytzev/adfilter/internal/pipeline"
	"github.com/belaytzev/adfilter/internal/render"
	"github.com/belaytzev/adfilter/internal/store"
)

// errInjection makes hidden-text findings visible to a shell: the document is
// still printed, but the exit code lets a script or a CI job refuse the source.
var errInjection = errors.New("hidden text found")

const (
	fetchTimeout = 20 * time.Second
	userAgent    = "adfilter/0.1 (+https://github.com/belaytzev/adfilter)"
	maxBody      = 8 << 20
)

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "calibrate" {
		return calibrate(args[1:], stdout)
	}

	fs := flag.NewFlagSet("adfilter", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adfilter [flags] [url]\n       adfilter calibrate [flags]\n\nWith no url the page is read from stdin.\n\nFlags:")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "print the full Result as JSON instead of markdown")
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dbPath := fs.String("db", "", "path to the local verdict database (default: user cache dir, $"+store.EnvDB+")")
	verbose := fs.Bool("verbose", false, "report hidden-text findings alongside the document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("adfilter: want at most one url, got %d", fs.NArg())
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	pageURL := fs.Arg(0)
	page, err := read(pageURL, stdin)
	if err != nil {
		return err
	}

	res, err := extract.Extract(page, pageURL, cfg.L1)
	if err != nil {
		return err
	}

	// Without a url there is no domain to count and nothing asked for a
	// database, so a stdin run stays a pure function of its input.
	var cache pipeline.Cache
	if pageURL != "" || *dbPath != "" {
		s, err := store.Open(*dbPath)
		if err != nil {
			return err
		}
		defer s.Close()
		if res.Domain != "" {
			if err := s.Visit(res.Domain, len(res.Hidden)); err != nil {
				return err
			}
		}
		cache = s
	}

	res, err = (&pipeline.Pipeline{Cfg: cfg, Cache: cache, Judge: judge.New(cfg.Judge)}).Run(res)
	if err != nil {
		return err
	}

	if err := write(stdout, res, *asJSON, *verbose); err != nil {
		return err
	}
	if len(res.Hidden) > 0 {
		return fmt.Errorf("adfilter: %w: %d finding(s)", errInjection, len(res.Hidden))
	}
	return nil
}

func read(pageURL string, stdin io.Reader) ([]byte, error) {
	if pageURL == "" {
		page, err := io.ReadAll(io.LimitReader(stdin, maxBody))
		if err != nil {
			return nil, fmt.Errorf("adfilter: read stdin: %w", err)
		}
		return page, nil
	}
	return fetch(pageURL)
}

func fetch(pageURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("adfilter: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := (&http.Client{Timeout: fetchTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adfilter: fetch %s: %s", pageURL, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch %s: %w", pageURL, err)
	}
	return page, nil
}

func write(w io.Writer, res core.Result, asJSON, verbose bool) error {
	if asJSON {
		return render.JSON(w, res)
	}
	if verbose {
		for _, f := range res.Hidden {
			if _, err := fmt.Fprintf(w, "# adfilter: hidden %s: %s\n", f.Kind, f.Sample); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w, render.Markdown(res))
	return err
}
