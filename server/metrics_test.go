package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"adassay.com/internal/core"
	"adassay.com/internal/store"
)

func scrape(t *testing.T, st *Store) map[string]float64 {
	t.Helper()
	w := httptest.NewRecorder()
	metricsMux(st, st.mx).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
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

	submitAs(t, mux, client(3), "", "", core.SubmitEntry{Hash: unknown, Verdict: core.Drop, Source: core.SourceRules})

	submitAs(t, mux, client(4), "", "", core.SubmitEntry{Hash: known, Verdict: core.Keep, Source: core.SourceRules})
	voteAs(t, mux, client(5), known, core.Keep)

	get(t, st, bucketPath(unknown[:core.PrefixLen], core.NormVersion))

	got := scrape(t, st)
	want := map[string]float64{
		`adassay_requests_total{endpoint="submit"}`: 4,
		`adassay_requests_total{endpoint="vote"}`:   1,
		"adassay_submit_divergent_total":            2,

		"adassay_verdicts_published":   1,
		"adassay_verdicts_quarantined": 1,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %v, want %v", name, got[name], v)
		}
	}

	if got[`adassay_requests_total{endpoint="bucket"}`] < 1 {
		t.Errorf("bucket requests = %v, want at least one", got[`adassay_requests_total{endpoint="bucket"}`])
	}
	if got["adassay_bucket_hits_total"] != 0 {
		t.Errorf("bucket hits = %v, want 0: the prefix read holds nothing published", got["adassay_bucket_hits_total"])
	}
}

func TestMetricsCountBucketHits(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("сегмент из базы")
	if err := st.put(core.BucketEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}, core.NormVersion); err != nil {
		t.Fatalf("put: %v", err)
	}

	get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))
	get(t, st, bucketPath("0000", core.NormVersion))

	got := scrape(t, st)
	if got["adassay_bucket_hits_total"] != 1 {
		t.Errorf("bucket hits = %v, want 1", got["adassay_bucket_hits_total"])
	}
	if got[`adassay_requests_total{endpoint="bucket"}`] != 2 {
		t.Errorf("bucket requests = %v, want 2", got[`adassay_requests_total{endpoint="bucket"}`])
	}
}

func TestMetricsAreNotOnThePublicListener(t *testing.T) {
	st := newTestStore(t)
	w := httptest.NewRecorder()
	testMux(st).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: /metrics on the public listener leaks database size, "+
			"quarantine depth and whether an attacker's submissions are landing", w.Code)
	}
}
