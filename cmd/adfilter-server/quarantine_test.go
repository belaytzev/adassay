package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/store"
)

// published reports whether the bucket endpoint serves a verdict for hash,
// which is the only view a client ever gets of the database.
func published(t *testing.T, st *Store, hash string) bool {
	t.Helper()
	for _, e := range decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))).Entries {
		if e.Hash == hash {
			return true
		}
	}
	return false
}

func submitAs(t *testing.T, mux http.Handler, clientID, remoteAddr, forwarded string, entries ...core.SubmitEntry) int {
	t.Helper()
	body, err := json.Marshal(core.SubmitRequest{ClientID: clientID, NormVersion: core.NormVersion, Entries: entries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/segments", strings.NewReader(string(body)))
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	if forwarded != "" {
		r.Header.Set("CF-Connecting-IP", forwarded)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code
}

func client(n int) string { return fmt.Sprintf("6f1c9f4e-2b8a-4c1d-9f3e-0a7b5c2d8e%02d", n) }

func TestVerdictLeavesQuarantineOnQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := store.HexHash("Материал подготовлен при поддержке")
	entry := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules}

	for i := 1; i <= st.quorum; i++ {
		if code := submitAs(t, mux, client(i), "", "", entry); code != http.StatusOK {
			t.Fatalf("submit %d: status = %d, want 200", i, code)
		}
		want := i >= st.quorum
		if got := published(t, st, hash); got != want {
			t.Fatalf("after %d confirmations published = %v, want %v", i, got, want)
		}
	}
}

// The attack the quarantine exists for: one installation inventing verdicts at
// volume. Repetition is not agreement, so nothing it says reaches other clients.
func TestFloodFromOneClientPublishesNothing(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)

	var hashes []string
	var entries []core.SubmitEntry
	for i := 0; i < 50; i++ {
		hash := store.HexHash(fmt.Sprintf("совершенно обычный абзац %d", i))
		hashes = append(hashes, hash)
		entries = append(entries, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})
	}
	// The cheap attack is the batch endpoint, not one request per verdict:
	// three calls are enough to claim fifty segments are advertising.
	for range 3 {
		if code := submitAs(t, mux, client(1), "", "", entries...); code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
	}
	for _, hash := range hashes {
		if published(t, st, hash) {
			t.Fatalf("hash %q was published on one client's word alone", hash)
		}
	}
}

// A verdict that gets overturned must serve its predecessor's confirmations to
// nobody: the new claim starts its quarantine from zero.
func TestOverturnedVerdictReentersQuarantine(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := store.HexHash("спорный абзац")

	for i := 1; i <= 2; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
	}
	if !published(t, st, hash) {
		t.Fatal("verdict with a quorum is not served")
	}

	submitAs(t, mux, client(9), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceHuman})
	if published(t, st, hash) {
		t.Fatal("overturned verdict is served on one client's word")
	}
	submitAs(t, mux, client(8), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceHuman})
	if !published(t, st, hash) {
		t.Fatal("re-confirmed verdict is still withheld")
	}
}

func TestWritesAreRateLimited(t *testing.T) {
	st := newTestStore(t)
	mux := testMux(st)
	entry := core.SubmitEntry{Hash: store.HexHash("любой сегмент"), Verdict: core.Drop, Source: core.SourceRules}

	for i := 0; i < writeBurst; i++ {
		if code := submitAs(t, mux, client(1), "203.0.113.7:4000", "", entry); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
	}
	if code := submitAs(t, mux, client(1), "203.0.113.7:4000", "", entry); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once the burst is spent", code)
	}
	// The limit is per address, not global: another client is unaffected.
	if code := submitAs(t, mux, client(2), "203.0.113.8:4000", "", entry); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a different address", code)
	}
	// Reads are never limited: a blocked lookup would push clients back onto
	// their own heuristics, which is the failure the database exists to fix.
	if w := get(t, st, bucketPath(entry.Hash[:core.PrefixLen], core.NormVersion)); w.Code != http.StatusOK {
		t.Fatalf("bucket status = %d, want 200", w.Code)
	}
}

// A forwarded-for header is trustworthy only from a proxy we run. From anyone
// else it is attacker-controlled: believing it lets one host mint a fresh
// identity, and a fresh rate limit, for every request it sends.
func TestForgedForwardedIPIsIgnored(t *testing.T) {
	st := newTestStore(t)
	entry := core.SubmitEntry{Hash: store.HexHash("любой сегмент"), Verdict: core.Drop, Source: core.SourceRules}

	untrusted, err := newGuard("192.0.2.10")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	mux := newMux(st, untrusted)
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", fmt.Sprintf("198.51.100.%d", i), entry)
		if i < writeBurst && code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
		if i == writeBurst && code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429: a forged CF-Connecting-IP reset the limit", code)
		}
	}

	trusted, err := newGuard("203.0.113.0/24")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	mux = newMux(st, trusted)
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", fmt.Sprintf("198.51.100.%d", i), entry)
		if code != http.StatusOK {
			t.Fatalf("request %d from a trusted proxy: status = %d, want 200", i, code)
		}
	}
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", "198.51.100.200", entry)
		if i == writeBurst && code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 for one client behind a trusted proxy", code)
		}
	}
}

func TestParseTrustedRejectsGarbage(t *testing.T) {
	if _, err := newGuard("not-an-address"); err == nil {
		t.Fatal("newGuard accepted a proxy list that is neither an address nor a CIDR")
	}
	g, err := newGuard(" 203.0.113.7 , 2001:db8::/32 ,")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	if len(g.trusted) != 2 {
		t.Fatalf("trusted = %v, want two prefixes", g.trusted)
	}
}
