package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/extract"
	"github.com/belaytzev/adfilter/internal/fetch"
	"github.com/belaytzev/adfilter/internal/pipeline"
	"github.com/belaytzev/adfilter/internal/share"
	"github.com/belaytzev/adfilter/internal/store"
)

const hashLen = 64

// vote is the human override against the shared database. A hash corrects one
// segment; a url corrects a page, voting on everything the filter did not keep
// — those are the calls a reader is in a position to confirm or deny.
func vote(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("adfilter vote", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adfilter vote <url|hash> --ad|--not-ad\n\nFlags:")
		fs.PrintDefaults()
	}
	isAd := fs.Bool("ad", false, "the segment is advertising")
	notAd := fs.Bool("not-ad", false, "the segment is not advertising")
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	endpoint := fs.String("share", "", "shared database endpoint (default: $"+share.EnvEndpoint+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *isAd == *notAd {
		return fmt.Errorf("adfilter vote: pass exactly one of --ad or --not-ad")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("adfilter vote: want one url or hash, got %d arguments", fs.NArg())
	}

	client := share.New(*endpoint)
	if client == nil {
		return fmt.Errorf("adfilter vote: no shared database configured, set $%s", share.EnvEndpoint)
	}
	verdict := core.Keep
	if *isAd {
		verdict = core.Drop
	}

	hashes, err := targets(fs.Arg(0), *cfgPath)
	if err != nil {
		return err
	}
	for _, h := range hashes {
		if err := client.Vote(h, verdict); err != nil {
			return fmt.Errorf("adfilter vote: %w", err)
		}
		fmt.Fprintf(stdout, "voted %s on %s\n", verdict, h)
	}
	if len(hashes) == 0 {
		fmt.Fprintln(stdout, "nothing to vote on: the page has no filtered segments")
	}
	return nil
}

// targets turns the argument into the hashes to vote on: a hash is itself, a
// url is fetched and filtered, and anything else is treated as segment text.
func targets(arg, cfgPath string) ([]string, error) {
	if isHash(arg) {
		return []string{strings.ToLower(arg)}, nil
	}
	if !strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "https://") {
		return []string{store.HexHash(arg)}, nil
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	page, err := fetch.Get(arg)
	if err != nil {
		return nil, err
	}
	res, err := extract.Extract(page, arg, cfg.L1)
	if err != nil {
		return nil, err
	}
	res, err = (&pipeline.Pipeline{Cfg: cfg}).Run(res)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for _, seg := range res.Segments {
		if seg.Verdict != core.Keep {
			hashes = append(hashes, store.HexHash(seg.Text))
		}
	}
	return hashes, nil
}

func isHash(s string) bool {
	if len(s) != hashLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
