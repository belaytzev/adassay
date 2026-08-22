package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/store"
)

// scrape returns the metrics endpoint parsed into name -> value, failing the
// test on anything Prometheus itself would reject: a sample without a HELP and
// TYPE line above it, or a value that is not a number.
func scrape(t *testing.T, mux http.Handler) map[string]float64 {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type = %q, want text/plain", ct)
	}

	out := map[string]float64{}
	declared := map[string]int{}
	for _, line := range strings.Split(strings.TrimRight(w.Body.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			fields := strings.Fields(line)
			if len(fields) < 4 || (fields[1] != "HELP" && fields[1] != "TYPE") {
				t.Fatalf("malformed comment %q", line)
			}
			declared[fields[2]]++
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("malformed sample %q", line)
		}
		if declared[strings.Split(name, "{")[0]] != 2 {
			t.Fatalf("sample %q has no HELP and TYPE above it", name)
		}
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("sample %q: value %q is not a number", name, value)
		}
		out[name] = v
	}
	return out
}

func TestMetricsReportTrafficAndDatabase(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)

	known := store.HexHash("Материал подготовлен при поддержке")
	unknown := store.HexHash("обычный абзац")
	for i := 1; i <= 2; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: known, Verdict: core.Drop, Source: core.SourceRules})
	}
	// One more verdict that nobody confirmed: it must show up as quarantined.
	submitAs(t, mux, client(3), "", "", core.SubmitEntry{Hash: unknown, Verdict: core.Drop, Source: core.SourceRules})
	// A disagreement the store refuses, and one it accepts: both are divergence.
	submitAs(t, mux, client(4), "", "", core.SubmitEntry{Hash: known, Verdict: core.Keep, Source: core.SourceRules})
	submitAs(t, mux, client(5), "", "", core.SubmitEntry{Hash: known, Verdict: core.Keep, Source: core.SourceHuman})

	get(t, st, bucketPath(unknown[:core.PrefixLen], core.NormVersion))

	got := scrape(t, mux)
	want := map[string]float64{
		`adfilter_requests_total{endpoint="submit"}`: 5,
		`adfilter_requests_total{endpoint="vote"}`:   0,
		"adfilter_submit_divergent_total":            2,
		// known was overturned by the human vote and lost its confirmations,
		// so both rows are back in quarantine.
		"adfilter_verdicts_published":   0,
		"adfilter_verdicts_quarantined": 2,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %v, want %v", name, got[name], v)
		}
	}
	// Bucket lookups: the published one hit, the quarantined one did not.
	if got[`adfilter_requests_total{endpoint="bucket"}`] < 1 {
		t.Errorf("bucket requests = %v, want at least one", got[`adfilter_requests_total{endpoint="bucket"}`])
	}
	if got["adfilter_bucket_hits_total"] != 0 {
		t.Errorf("bucket hits = %v, want 0: nothing is published", got["adfilter_bucket_hits_total"])
	}
}

func TestMetricsCountBucketHits(t *testing.T) {
	st := newTestStore(t)
	mux := testMux(st)
	hash := store.HexHash("сегмент из базы")
	if err := st.put(core.BucketEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}, core.NormVersion); err != nil {
		t.Fatalf("put: %v", err)
	}

	get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))
	get(t, st, bucketPath("0000", core.NormVersion))

	got := scrape(t, mux)
	if got["adfilter_bucket_hits_total"] != 1 {
		t.Errorf("bucket hits = %v, want 1", got["adfilter_bucket_hits_total"])
	}
	if got[`adfilter_requests_total{endpoint="bucket"}`] != 2 {
		t.Errorf("bucket requests = %v, want 2", got[`adfilter_requests_total{endpoint="bucket"}`])
	}
}
