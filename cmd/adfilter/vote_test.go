package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestVoteNeedsEndpoint(t *testing.T) {
	t.Setenv(share.EnvEndpoint, "")
	var out bytes.Buffer
	if err := run([]string{"vote", "--ad", "some text"}, strings.NewReader(""), &out); err == nil {
		t.Error("want an error without a configured endpoint")
	}
}
