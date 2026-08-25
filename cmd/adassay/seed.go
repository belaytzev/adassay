package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"time"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/fetch"
	"adassay.com/internal/judge"
	"adassay.com/internal/pipeline"
	"adassay.com/internal/share"
	"adassay.com/internal/store"
)

const seedPause = 5 * time.Second

type feedItem struct {
	Link      string `xml:"link"`
	PubDate   string `xml:"pubDate"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

type atomEntry struct {
	Links     []atomLink `xml:"link"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
}

// An Atom entry carries several links — the article, its comment feed, its
// comment page. Without rel the last one wins, which is usually the comments.
func (e atomEntry) article() string {
	for _, l := range e.Links {
		if l.Rel == "alternate" {
			return l.Href
		}
	}
	for _, l := range e.Links {
		if l.Rel == "" {
			return l.Href
		}
	}
	return ""
}

type feed struct {
	Items   []feedItem  `xml:"channel>item"`
	Entries []atomEntry `xml:"entry"`
}

func seed(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("adassay seed", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adassay seed [flags]\n\nWalks recent articles from feeds and submits verdicts as source=seed.\nThe install must be marked as a seeder on the server.\n\nFlags:")
		fs.PrintDefaults()
	}
	feeds := fs.String("feeds", "", "file with one feed URL per line, # for comments")
	urls := fs.String("urls", "", "file with page URLs to visit directly, one per line")
	since := fs.Duration("since", 24*time.Hour, "only visit articles published within this window")
	limit := fs.Int("limit", 100, "stop after this many pages")
	pause := fs.Duration("pause", seedPause, "wait between pages, so a publisher sees a reader rather than a crawler")
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dbPath := fs.String("db", "", "path to the local verdict database")
	dry := fs.Bool("dry-run", false, "print what would be visited and submit nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *feeds == "" && *urls == "" {
		return fmt.Errorf("adassay seed: give --feeds or --urls")
	}
	if *limit < 1 {
		return fmt.Errorf("adassay seed: --limit must be at least 1, got %d", *limit)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	targets, err := collect(*feeds, *urls, *since)
	if err != nil {
		return err
	}
	rand.Shuffle(len(targets), func(i, j int) { targets[i], targets[j] = targets[j], targets[i] })
	if len(targets) > *limit {
		targets = targets[:*limit]
	}
	fmt.Fprintf(stdout, "seed: %d pages to visit\n", len(targets))
	if *dry {
		for _, u := range targets {
			fmt.Fprintln(stdout, " ", u)
		}
		return nil
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	client := share.New("")
	if client == nil {
		return fmt.Errorf("adassay seed: no endpoint, set $%s", share.EnvEndpoint)
	}
	out := share.NewOutbox(st, client, false)
	if out == nil {
		return fmt.Errorf("adassay seed: sharing is off, unset $%s", share.EnvOptOut)
	}
	out.Source = core.SourceSeed
	p := &pipeline.Pipeline{Cfg: cfg, Cache: st, Shared: client, Judge: judge.New(cfg.Judge)}

	var visited, queued, failed int
	for i, u := range targets {
		if i > 0 {
			time.Sleep(*pause)
		}
		res, err := visit(u, cfg, st, p)
		if err != nil {
			failed++
			fmt.Fprintf(stdout, "  %s: %v\n", short(u), err)
			continue
		}
		visited++
		n := 0
		for _, s := range res.Segments {
			if s.Verdict == core.Drop {
				n++
			}
		}
		queued += out.Record(res, p.Adopted)
		fmt.Fprintf(stdout, "  %s: %d segments, %d dropped, %d hidden\n", short(u), len(res.Segments), n, len(res.Hidden))
	}

	fmt.Fprintf(stdout, "seed: %d visited, %d failed, %d verdicts queued\n", visited, failed, queued)
	return out.FlushNow()
}

func visit(pageURL string, cfg *config.Config, st *store.Store, p *pipeline.Pipeline) (core.Result, error) {
	page, err := fetch.Get(pageURL)
	if err != nil {
		return core.Result{}, err
	}
	res, err := extract.Extract(page, pageURL, cfg.L1)
	if err != nil {
		return core.Result{}, err
	}
	if res.Domain != "" {
		if err := st.Visit(res.Domain, len(res.Hidden)); err != nil {
			return core.Result{}, err
		}
	}
	return p.Run(res)
}

func collect(feedsFile, urlsFile string, since time.Duration) ([]string, error) {
	seen := map[string]bool{}
	var out []string

	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") {
			return
		}
		seen[u] = true
		out = append(out, u)
	}

	if urlsFile != "" {
		lines, err := readLines(urlsFile)
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			add(l)
		}
	}

	if feedsFile != "" {
		lines, err := readLines(feedsFile)
		if err != nil {
			return nil, err
		}
		cutoff := time.Now().Add(-since)
		for _, f := range lines {
			links, err := feedLinks(f, cutoff)
			if err != nil {
				fmt.Fprintf(os.Stderr, "seed: %s: %v\n", short(f), err)
				continue
			}
			for _, l := range links {
				add(l)
			}
		}
	}
	return out, nil
}

func feedLinks(feedURL string, cutoff time.Time) ([]string, error) {
	body, err := fetch.Get(feedURL)
	if err != nil {
		return nil, err
	}
	var f feed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	var out []string
	for _, it := range f.Items {
		if fresh(cutoff, it.PubDate, it.Published, it.Updated) {
			out = append(out, it.Link)
		}
	}
	for _, e := range f.Entries {
		if fresh(cutoff, e.Published, e.Updated) {
			out = append(out, e.article())
		}
	}
	if seen := len(f.Items) + len(f.Entries); seen > 0 && len(out) == 0 {
		fmt.Fprintf(os.Stderr, "seed: %s: %d entries, none inside the window or none carrying a date\n",
			short(feedURL), seen)
	}
	return out, nil
}

// An entry nobody can date is not evidence that it is fresh: taking it anyway
// makes --since inert for the whole feed and pulls its backlog in.
func fresh(cutoff time.Time, stamps ...string) bool {
	layouts := []string{
		time.RFC1123Z, time.RFC1123, time.RFC3339,
		time.RFC822Z, time.RFC822, time.DateOnly,
		"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	}
	for _, s := range stamps {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, s); err == nil {
				return t.After(cutoff)
			}
		}
	}
	return false
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("adassay seed: %w", err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

func short(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	s := p.Host + p.Path
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}
