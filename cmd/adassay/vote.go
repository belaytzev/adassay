package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"strings"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/fetch"
	"adassay.com/internal/judge"
	"adassay.com/internal/pipeline"
	"adassay.com/internal/share"
	"adassay.com/internal/store"
)

const hashLen = 64

func vote(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("adassay vote", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adassay vote <url|hash|segment text> --ad|--not-ad\n\nA url votes on everything the filter did not keep, a 64-hex hash on that one\nsegment, and anything else is taken as the exact text of a segment.\n\nFlags:")
		fs.PrintDefaults()
	}
	isAd := fs.Bool("ad", false, "the segment is advertising")
	notAd := fs.Bool("not-ad", false, "the segment is not advertising")
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dbPath := fs.String("db", "", "path to the local verdict database (default: user cache dir, $"+store.EnvDB+")")
	endpoint := fs.String("share", "", "shared database endpoint (default: $"+share.EnvEndpoint+")")
	rest, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if *isAd == *notAd {
		return fmt.Errorf("adassay vote: pass exactly one of --ad or --not-ad")
	}
	if len(rest) != 1 {
		return fmt.Errorf("adassay vote: want one url or hash, got %d arguments", len(rest))
	}

	client := share.New(*endpoint)
	verdict := core.Keep
	if *isAd {
		verdict = core.Drop
	}

	hashes, err := targets(rest[0], *cfgPath, *dbPath, client)
	if err != nil {
		return err
	}

	local, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer local.Close()

	if err := local.ClearPending(hashes); err != nil {
		return err
	}

	for _, h := range hashes {
		raw, err := hex.DecodeString(h)
		if err != nil {
			return fmt.Errorf("adassay vote: %w", err)
		}
		if err := local.Upsert(store.Record{Hash: raw, Verdict: verdict, Source: core.SourceHuman}); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "voted %s on %s\n", verdict, h)
	}
	if len(hashes) == 0 {
		fmt.Fprintln(stdout, "nothing to vote on: the page has no filtered segments")
		return nil
	}
	if client == nil {
		fmt.Fprintf(stdout, "local only: no shared database configured, set $%s to send votes\n", share.EnvEndpoint)
		return nil
	}
	for _, h := range hashes {
		if err := client.Vote(h, verdict); err != nil {
			return fmt.Errorf("adassay vote: corrected locally, sending %s failed: %w", h, err)
		}
	}
	return nil
}

func targets(arg, cfgPath, dbPath string, client *share.Client) ([]string, error) {
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
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	res, err = (&pipeline.Pipeline{Cfg: cfg, Cache: s, Shared: client, Judge: judge.New(cfg.Judge)}).Run(res)
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
