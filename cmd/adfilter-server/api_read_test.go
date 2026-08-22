package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/store"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "nested", "shared.db"))
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func get(t *testing.T, st *Store, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	newMux(st).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func bucketPath(prefix string, version int) string {
	return "/v1/segments/" + prefix + "?norm_version=" + strconv.Itoa(version)
}

func decodeBucket(t *testing.T, w *httptest.ResponseRecorder) core.BucketResponse {
	t.Helper()
	var got core.BucketResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return got
}

func TestBucketReturnsStoredVerdict(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("Партнёрский материал")
	entry := core.BucketEntry{
		Hash:    hash,
		Verdict: core.Drop,
		Reasons: []string{"disclaimer"},
		Source:  core.SourceRules,
		Votes:   2,
	}
	if err := st.put(entry, core.NormVersion); err != nil {
		t.Fatalf("put: %v", err)
	}

	prefix := store.Prefix(store.Hash("Партнёрский материал"))
	w := get(t, st, bucketPath(prefix, core.NormVersion))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	got := decodeBucket(t, w)
	if got.Prefix != prefix || got.NormVersion != core.NormVersion {
		t.Fatalf("response = %+v, want the requested prefix and version", got)
	}

	var found *core.BucketEntry
	for i := range got.Entries {
		if got.Entries[i].Hash == hash {
			found = &got.Entries[i]
		}
	}
	if found == nil {
		t.Fatalf("entries = %+v, want the stored hash", got.Entries)
	}
	if found.Verdict != core.Drop || found.Source != core.SourceRules || found.Votes != 2 ||
		len(found.Reasons) != 1 || found.Reasons[0] != "disclaimer" {
		t.Fatalf("entry = %+v, want the row that was written", *found)
	}
}

// A bucket of one is not anonymity: it names the segment that was looked up.
func TestBucketIsPaddedAndStable(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("одна запись")
	if err := st.put(core.BucketEntry{Hash: hash, Verdict: core.Flag, Source: core.SourceRules}, core.NormVersion); err != nil {
		t.Fatalf("put: %v", err)
	}
	prefix := hash[:core.PrefixLen]

	first := decodeBucket(t, get(t, st, bucketPath(prefix, core.NormVersion)))
	if len(first.Entries) < core.MinBucket {
		t.Fatalf("entries = %d, want at least %d", len(first.Entries), core.MinBucket)
	}
	seen := map[string]bool{}
	for _, e := range first.Entries {
		if len(e.Hash) != 64 || e.Hash[:core.PrefixLen] != prefix {
			t.Fatalf("entry hash %q must be a full hash under the requested prefix", e.Hash)
		}
		if seen[e.Hash] {
			t.Fatalf("duplicate hash %q in bucket", e.Hash)
		}
		seen[e.Hash] = true
	}

	// Two lookups of the same bucket must agree; a fresh set of decoys each
	// time would expose the real rows to anyone diffing the responses.
	second := decodeBucket(t, get(t, st, bucketPath(prefix, core.NormVersion)))
	if len(second.Entries) != len(first.Entries) {
		t.Fatalf("second bucket has %d entries, first had %d", len(second.Entries), len(first.Entries))
	}
	for i := range first.Entries {
		if first.Entries[i].Hash != second.Entries[i].Hash {
			t.Fatalf("bucket is not stable: %q then %q", first.Entries[i].Hash, second.Entries[i].Hash)
		}
	}

	empty := decodeBucket(t, get(t, st, bucketPath("0000", core.NormVersion)))
	if len(empty.Entries) < core.MinBucket {
		t.Fatalf("empty bucket returned %d entries, want at least %d", len(empty.Entries), core.MinBucket)
	}
}

func TestBucketRejectsBadRequests(t *testing.T) {
	st := newTestStore(t)
	prefix := store.Prefix(store.Hash("что угодно"))

	cases := []struct {
		name string
		path string
	}{
		{"non-hex prefix", bucketPath(strings.Repeat("z", core.PrefixLen), core.NormVersion)},
		{"uppercase prefix", bucketPath(strings.Repeat("A", core.PrefixLen), core.NormVersion)},
		{"short prefix", bucketPath(prefix[:core.PrefixLen-1], core.NormVersion)},
		{"long prefix", bucketPath(prefix+"a", core.NormVersion)},
		{"foreign norm version", bucketPath(prefix, core.NormVersion+1)},
		{"missing norm version", "/v1/segments/" + prefix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, st, tc.path)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}
			var e core.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Fatalf("body = %q, want an error message", w.Body.String())
			}
		})
	}
}

// The prefix the client is allowed to send and the prefix the server accepts
// are the same constant, or every lookup misses.
func TestPrefixLengthMatchesClient(t *testing.T) {
	st := newTestStore(t)
	prefix := store.Prefix(store.Hash("контрактный тест"))
	if len(prefix) != core.PrefixLen {
		t.Fatalf("store.Prefix returned %d chars, core.PrefixLen is %d", len(prefix), core.PrefixLen)
	}
	if w := get(t, st, bucketPath(prefix, core.NormVersion)); w.Code != http.StatusOK {
		t.Fatalf("server rejected a client-built prefix %q: %d %s", prefix, w.Code, w.Body)
	}
}
