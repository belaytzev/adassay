package judge

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

func serveSystemOne(t *testing.T, probs map[string]float64) (*Judge, *[]systemOneRequest) {
	t.Helper()
	var reqs []systemOneRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("path = %q, want /v1/systemone", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tk-test" {
			t.Errorf("Authorization = %q, want the key as a bearer", got)
		}
		var req systemOneRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		reqs = append(reqs, req)
		answers := map[string]any{}
		for id := range req.Questions {
			if p, ok := probs[id]; ok {
				answers[id] = map[string]float64{"noul": p}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.14.0", "answers": answers})
	}))
	t.Cleanup(srv.Close)

	j := New(config.Judge{
		API: config.APISystemOne, Endpoint: srv.URL, Model: "jev-1.14.0", Timeout: 5 * time.Second,
		BatchSize: 1, DropAt: 0.8, KeepAt: 0.25, APIKey: "tk-test",
	})
	j.Log = slog.New(slog.DiscardHandler)
	return j, &reqs
}

func TestSystemOneProbabilityBecomesVerdict(t *testing.T) {
	j, reqs := serveSystemOne(t, map[string]float64{"s1": 0.97, "s2": 0.04, "s3": 0.5, "s4": 0.8, "s5": 0.25})

	got := j.Decide("best robot vacuums", segs("s1", "s2", "s3", "s4", "s5", "s6"))

	want := map[string]core.Verdict{"s1": core.Drop, "s2": core.Keep, "s3": core.Flag, "s4": core.Drop, "s5": core.Keep}
	for id, v := range want {
		if got[id] != v {
			t.Errorf("%s = %v, want %v", id, got[id], v)
		}
	}
	if _, ok := got["s6"]; ok {
		t.Error("s6 got a verdict without an answer: core.Keep is the zero value, so a missing answer must stay missing")
	}

	if len(*reqs) != 1 {
		t.Fatalf("requests = %d, want 1: batch_size is for chat models, systemone takes the page at once", len(*reqs))
	}
	req := (*reqs)[0]
	page := req.State["page"].(map[string]any)
	if page["title"] != "best robot vacuums" {
		t.Errorf("title = %v, want the page title in state", page["title"])
	}
	if frags := page["fragments"].(map[string]any); frags["s3"] != "fragment s3" {
		t.Errorf("fragments = %v, want the text keyed by segment id", frags)
	}
	q := req.Questions["s3"]
	if q.Type != "noul" || !strings.Contains(q.Instructions["question"], "`page.fragments.s3`") {
		t.Errorf("question for s3 = %+v, want a noul pointing at its own fragment", q)
	}
}

func TestSystemOneSplitsLargeGreyZones(t *testing.T) {
	j, reqs := serveSystemOne(t, nil)
	ids := make([]string, systemOneBatch+5)
	for i := range ids {
		ids[i] = fmt.Sprintf("s%d", i+1)
	}

	j.Decide("", segs(ids...))

	if len(*reqs) != 2 {
		t.Errorf("requests = %d, want 2 for %d segments", len(*reqs), len(ids))
	}
}

func TestSystemOneRefusalLeavesGreyZone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid key", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	j := New(config.Judge{
		API: config.APISystemOne, Endpoint: srv.URL, Model: "jev-1.14.0", Timeout: time.Second,
		BatchSize: 8, DropAt: 0.8, KeepAt: 0.25,
	})
	j.Log = slog.New(slog.DiscardHandler)

	if got := j.Decide("", segs("s1")); len(got) != 0 {
		t.Errorf("got %v, want nothing: a missing key must not turn the grey zone into keeps", got)
	}
}

// Off by default, like the gateway test: it costs money and needs the network.
//
//	ADASSAY_JUDGE_KEY=... go test ./internal/judge -run SystemOneLive -v
func TestSystemOneLive(t *testing.T) {
	key := os.Getenv(config.EnvJudgeKey)
	if key == "" {
		t.Skipf("set %s to a TypeSafe key to run this", config.EnvJudgeKey)
	}
	j := New(config.Judge{
		API: config.APISystemOne, Endpoint: "https://api.typesafe.ai", Model: "jev-1.14.0",
		Timeout: 30 * time.Second, BatchSize: 8, DropAt: 0.8, KeepAt: 0.25, APIKey: key,
	})
	j.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))

	got := j.Decide("a review of kitchen blenders", []core.Segment{
		{ID: "s1", Text: "Readers of this site get 20% off with code BLEND20 at checkout — our thanks to our partner.", Verdict: core.Flag},
		{ID: "s2", Text: "A blade grinder heats the beans unevenly, which is why the grind tastes flat.", Verdict: core.Flag},
	})

	if v, ok := got["s1"]; !ok || v != core.Drop {
		t.Errorf("s1 = %v (answered %v), want drop", v, ok)
	}
	if v, ok := got["s2"]; !ok || v != core.Keep {
		t.Errorf("s2 = %v (answered %v), want keep", v, ok)
	}
}
