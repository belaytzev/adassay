package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/share"
	"github.com/belaytzev/adfilter/internal/store"
)

func TestVoteSendsHashOfText(t *testing.T) {
	var got core.VoteRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode vote: %v", err)
		}
		json.NewEncoder(w).Encode(core.VoteResponse{Hash: got.Hash, Votes: 1})
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())

	const text = "Use code SAVE20 at checkout for our sponsor."
	var out bytes.Buffer
	if err := run([]string{"vote", "--share", srv.URL, "--ad", text}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if got.Hash != store.HexHash(text) || got.Verdict != core.Drop {
		t.Errorf("vote arrived as %+v", got)
	}
}

// TestVoteReachesCachedVerdict is the point of voting by url: the verdicts a
// reader wants overturned are the ones an authority above the rules produced,
// and re-running the rules alone would never put them on the ballot.
func TestVoteReachesCachedVerdict(t *testing.T) {
	const text = "Water just off the boil, around ninety four degrees, keeps the bitterness down while still pulling enough of the sweetness out of a medium roast."

	db := filepath.Join(t.TempDir(), "verdicts.db")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(store.Record{
		Hash:    store.Hash(text),
		Verdict: core.Drop,
		Reasons: []string{"judge"},
		Source:  core.SourceOllama,
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	var votes []core.VoteRequest
	share := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The same endpoint answers the pipeline's bucket lookups; only the
		// votes are of interest here.
		if r.Method != http.MethodPost {
			http.Error(w, "{}", http.StatusNotFound)
			return
		}
		var req core.VoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode vote: %v", err)
		}
		votes = append(votes, req)
		json.NewEncoder(w).Encode(core.VoteResponse{Hash: req.Hash, Votes: 1})
	}))
	defer share.Close()

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, cleanPage)
	}))
	defer page.Close()

	var out bytes.Buffer
	args := []string{"vote", "--config", filepath.Join("testdata", "offline.yaml"),
		"--share", share.URL, "--db", db, "--not-ad", page.URL}
	if err := run(args, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}
	for _, v := range votes {
		if v.Hash == store.HexHash(text) && v.Verdict == core.Keep {
			return
		}
	}
	t.Fatalf("cached drop never reached the ballot, votes: %+v", votes)
}

// A vote takes effect for the reader who cast it right away: overturning the
// shared database needs a quorum of installations, and until it forms the local
// run must already reflect the correction.
func TestVoteOverridesLocally(t *testing.T) {
	const text = "Use code SAVE20 at checkout for our sponsor."
	db := filepath.Join(t.TempDir(), "verdicts.db")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req core.VoteRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(core.VoteResponse{Hash: req.Hash, Votes: 1})
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := run([]string{"vote", "--share", srv.URL, "--db", db, "--not-ad", text}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}

	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, ok, err := s.Lookup(store.Hash(text))
	if err != nil || !ok {
		t.Fatalf("lookup: %v, found = %v; want the vote cached locally", err, ok)
	}
	if rec.Verdict != core.Keep || rec.Source != core.SourceHuman {
		t.Fatalf("cached record = %+v, want a human keep", rec)
	}
}

func TestVoteRejectsAmbiguousFlags(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{
		{"vote", "--ad", "--not-ad", "some text"},
		{"vote", "some text"},
		{"vote", "--ad"},
	} {
		if err := run(args, strings.NewReader(""), &out); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

// The documented form puts the target first: `adfilter vote <text> --ad`. Go's
// flag package stops at the first non-flag, so without interleaved parsing every
// flag after the target — the verdict included — would be read as an argument.
func TestVoteAcceptsFlagsAfterTarget(t *testing.T) {
	const text = "Use code SAVE20 at checkout for our sponsor."
	t.Setenv(share.EnvEndpoint, "")
	db := filepath.Join(t.TempDir(), "verdicts.db")

	var out bytes.Buffer
	if err := run([]string{"vote", text, "--ad", "--db", db}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}

	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, ok, err := s.Lookup(store.Hash(text))
	if err != nil || !ok {
		t.Fatalf("lookup: %v, found = %v; want the vote in the database named by --db", err, ok)
	}
	if rec.Verdict != core.Drop {
		t.Fatalf("cached record = %+v, want the drop the trailing --ad asked for", rec)
	}
}

// Without a shared database the vote is still the reader's override of the
// local one: the install that shares nothing is the default install, and there
// the human source would otherwise be unreachable from every command.
func TestVoteWithoutEndpointStaysLocal(t *testing.T) {
	const text = "Use code SAVE20 at checkout for our sponsor."
	t.Setenv(share.EnvEndpoint, "")
	db := filepath.Join(t.TempDir(), "verdicts.db")

	var out bytes.Buffer
	if err := run([]string{"vote", "--db", db, "--ad", text}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if !strings.Contains(out.String(), "local only") {
		t.Errorf("output = %q, want it to say the vote went nowhere else", out.String())
	}

	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, ok, err := s.Lookup(store.Hash(text))
	if err != nil || !ok {
		t.Fatalf("lookup: %v, found = %v; want the vote cached locally", err, ok)
	}
	if rec.Verdict != core.Drop || rec.Source != core.SourceHuman {
		t.Fatalf("cached record = %+v, want a human drop", rec)
	}
}

// A queued drop for the same hash must not survive the vote: the backend keeps
// one opinion per client per hash, so a later flush would undo the correction.
func TestVoteClearsQueuedVerdict(t *testing.T) {
	const text = "Use code SAVE20 at checkout for our sponsor."
	db := filepath.Join(t.TempDir(), "verdicts.db")

	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(core.SubmitEntry{
		Hash:    store.HexHash(text),
		Verdict: core.Drop,
		Reasons: []string{"promo_code"},
		Source:  core.SourceRules,
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req core.VoteRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(core.VoteResponse{Hash: req.Hash, Votes: 1})
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := run([]string{"vote", "--share", srv.URL, "--db", db, "--not-ad", text}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("vote: %v", err)
	}

	s, err = store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, _, err := s.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("outbox still holds %+v after the vote", pending)
	}
}
