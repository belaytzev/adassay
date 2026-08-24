package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/fetch"
	"adassay.com/internal/judge"
	"adassay.com/internal/pipeline"
	"adassay.com/internal/render"
	"adassay.com/internal/share"
	"adassay.com/internal/store"
)

var (
	errInjection = errors.New("hidden text found")
	errThin      = errors.New("extraction looks incomplete")
)

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "calibrate":
			return calibrate(args[1:], stdout)
		case "seed":
			return seed(args[1:], stdout)
		case "vote":
			return vote(args[1:], stdout)
		}
	}

	fs := flag.NewFlagSet("adassay", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adassay [flags] [url]\n       adassay calibrate [flags]\n       adassay vote <url|hash|segment text> --ad|--not-ad\n\nWith no url the page is read from stdin.\n\nFlags:")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "print the full Result as JSON instead of markdown")
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dbPath := fs.String("db", "", "path to the local verdict database (default: user cache dir, $"+store.EnvDB+")")
	verbose := fs.Bool("verbose", false, "report hidden-text findings alongside the document")
	noShare := fs.Bool("no-share", false, "never send verdicts to the shared database ($"+share.EnvOptOut+")")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return fmt.Errorf("adassay: want at most one url, got %d", len(rest))
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	var pageURL string
	if len(rest) == 1 {
		pageURL = rest[0]
	}
	page, err := read(pageURL, stdin)
	if err != nil {
		return err
	}

	res, err := extract.Extract(page, pageURL, cfg.L1)
	if err != nil {
		return err
	}

	var cache pipeline.Cache
	var outbox *share.Outbox
	var client *share.Client
	if pageURL != "" || *dbPath != "" {
		client = share.New("")
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

		outbox = share.NewOutbox(s, client, *noShare)
		outbox.Flush()
	}

	p := &pipeline.Pipeline{Cfg: cfg, Cache: cache, Shared: client, Judge: judge.New(cfg.Judge)}
	res, err = p.Run(res)
	if err != nil {
		return err
	}
	outbox.Record(res, p.Adopted)

	if err := write(stdout, res, *asJSON, *verbose); err != nil {
		return err
	}
	if len(res.Hidden) > 0 {
		return fmt.Errorf("adassay: %w: %d finding(s)", errInjection, len(res.Hidden))
	}
	if res.Thin {
		return fmt.Errorf("adassay: %w: %d segment(s), %d of %d visible characters kept", errThin, len(res.Segments), len([]rune(res.Text)), res.Visible)
	}
	return nil
}

func read(pageURL string, stdin io.Reader) ([]byte, error) {
	if pageURL == "" {
		page, err := io.ReadAll(io.LimitReader(stdin, fetch.MaxBody))
		if err != nil {
			return nil, fmt.Errorf("adassay: read stdin: %w", err)
		}
		return page, nil
	}
	return fetch.Get(pageURL)
}

func write(w io.Writer, res core.Result, asJSON, verbose bool) error {
	if asJSON {
		return render.JSON(w, res)
	}
	if verbose {

		for _, f := range res.Hidden {
			if _, err := fmt.Fprintf(w, "# adassay: hidden %s: %s\n", f.Kind, render.Sample(f.Sample)); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w, render.Markdown(res))
	return err
}
