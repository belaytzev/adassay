package judge

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/core"
)

func segs(ids ...string) []core.Segment {
	out := make([]core.Segment, len(ids))
	for i, id := range ids {
		out[i] = core.Segment{ID: id, Text: "fragment " + id, Verdict: core.Flag}
	}
	return out
}

// serve replies to /api/generate with the given raw model payloads, one per
// request in order.
func serve(t *testing.T, payloads ...string) (*Judge, *[]string) {
	t.Helper()
	var prompts []string
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %q, want /api/generate", r.URL.Path)
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Stream {
			t.Error("stream must be off: the answer is parsed as one object")
		}
		if req.Format != "json" {
			t.Errorf("format = %q, want json", req.Format)
		}
		prompts = append(prompts, req.Prompt)
		payload := payloads[min(n, len(payloads)-1)]
		n++
		fmt.Fprint(w, `{"response":`+quote(payload)+`}`)
	}))
	t.Cleanup(srv.Close)

	j := New(config.Judge{Endpoint: srv.URL, Model: "test", Timeout: 5 * time.Second, BatchSize: 8})
	j.Log = slog.New(slog.DiscardHandler)
	return j, &prompts
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestParse(t *testing.T) {
	tests := map[string]struct {
		payload string
		want    map[string]core.Verdict
		wantErr bool
	}{
		"valid": {
			payload: `{"verdicts":[{"id":"s1","verdict":"drop"},{"id":"s2","verdict":"keep"}]}`,
			want:    map[string]core.Verdict{"s1": core.Drop, "s2": core.Keep},
		},
		"broken json": {payload: `{"verdicts":[{"id":`, wantErr: true},
		"not json":    {payload: "I think the second one is an ad.", wantErr: true},
		"empty":       {payload: `{"verdicts":[]}`, want: map[string]core.Verdict{}},
		"missing key": {payload: `{"answers":[{"id":"s1","verdict":"drop"}]}`, want: map[string]core.Verdict{}},
		"unknown verdict": {
			payload: `{"verdicts":[{"id":"s1","verdict":"advertisement"},{"id":"s2","verdict":"flag"}]}`,
			want:    map[string]core.Verdict{"s2": core.Flag},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(tc.payload)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got %v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("%s = %v, want %v", id, got[id], want)
				}
			}
		})
	}
}

// The whole point of matching by id: a model that answers out of order and
// skips one fragment must not slide verdicts onto the neighbours.
func TestShuffledIncompleteAnswerKeepsIdsStraight(t *testing.T) {
	j, _ := serve(t, `{"verdicts":[
		{"id":"s3","verdict":"drop"},
		{"id":"s1","verdict":"keep"}
	]}`)

	got := j.Decide("grinders", segs("s1", "s2", "s3"))

	if got["s1"] != core.Keep {
		t.Errorf("s1 = %v, want keep", got["s1"])
	}
	if got["s3"] != core.Drop {
		t.Errorf("s3 = %v, want drop", got["s3"])
	}
	if _, ok := got["s2"]; ok {
		t.Errorf("s2 = %v, want no verdict: the model said nothing about it", got["s2"])
	}
}

func TestUnknownIdsAreDropped(t *testing.T) {
	j, _ := serve(t, `{"verdicts":[{"id":"s1","verdict":"drop"},{"id":"s99","verdict":"drop"}]}`)

	got := j.Decide("", segs("s1"))

	if len(got) != 1 || got["s1"] != core.Drop {
		t.Errorf("got %v, want only s1", got)
	}
}

func TestUnavailableOllamaDegrades(t *testing.T) {
	tests := map[string]*Judge{
		"connection refused": New(config.Judge{
			Endpoint: "http://127.0.0.1:1", Model: "test", Timeout: time.Second, BatchSize: 8,
		}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	tests["http error"] = New(config.Judge{
		Endpoint: srv.URL, Model: "test", Timeout: time.Second, BatchSize: 8,
	})

	broken, _ := serve(t, `not json at all`)
	tests["broken answer"] = broken

	for name, j := range tests {
		t.Run(name, func(t *testing.T) {
			j.Log = slog.New(slog.DiscardHandler)
			if got := j.Decide("", segs("s1", "s2")); len(got) != 0 {
				t.Errorf("got %v, want nothing: an unusable judge leaves the grey zone alone", got)
			}
		})
	}
}

func TestBatching(t *testing.T) {
	j, prompts := serve(t,
		`{"verdicts":[{"id":"s1","verdict":"drop"}]}`,
		`{"verdicts":[{"id":"s3","verdict":"drop"}]}`,
		`{"verdicts":[{"id":"s5","verdict":"keep"}]}`)
	j.Cfg.BatchSize = 2

	got := j.Decide("", segs("s1", "s2", "s3", "s4", "s5"))

	if len(*prompts) != 3 {
		t.Fatalf("requests = %d, want 3 for 5 segments at batch size 2", len(*prompts))
	}
	if len(got) != 3 {
		t.Errorf("got %v, want a verdict from each batch", got)
	}
	// A batch only ever sees its own ids, so an answer cannot leak across.
	if strings.Contains((*prompts)[0], "id: s3") {
		t.Errorf("first batch carried a later segment:\n%s", (*prompts)[0])
	}
}

func TestPromptCarriesTopicAndIds(t *testing.T) {
	j, prompts := serve(t, `{"verdicts":[]}`)

	j.Decide("choosing a coffee grinder", segs("s1", "s2"))

	p := (*prompts)[0]
	for _, want := range []string{"choosing a coffee grinder", "id: s1", "id: s2", "inform", "sell"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestLongSegmentIsTruncated(t *testing.T) {
	long := strings.Repeat("a", maxSegmentRunes+500)
	got := Build("", []core.Segment{{ID: "s1", Text: long}})

	if strings.Contains(got, long) {
		t.Error("a long segment reached the model untruncated")
	}
	if !strings.Contains(got, "...") {
		t.Error("truncation must be visible to the model")
	}
}

// The request body must be a plain non-streaming generate call: a streamed
// answer would arrive as several JSON objects and parse as garbage.
func TestRequestShape(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"response":"{\"verdicts\":[]}"}`)
	}))
	t.Cleanup(srv.Close)

	j := New(config.Judge{Endpoint: srv.URL + "/", Model: "qwen", Timeout: time.Second, BatchSize: 4})
	j.Decide("", segs("s1"))

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("body: %v", err)
	}
	if req["model"] != "qwen" {
		t.Errorf("model = %v, want qwen", req["model"])
	}
	if req["stream"] != false {
		t.Errorf("stream = %v, want false", req["stream"])
	}
}

// A model that failed once will not answer the next batch either, and each
// attempt costs the full timeout while the reader waits for the document.
func TestFailedBatchStopsTheRound(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	j := New(config.Judge{Endpoint: srv.URL, Model: "test", Timeout: time.Second, BatchSize: 1})
	j.Log = slog.New(slog.DiscardHandler)
	if got := j.Decide("", segs("s1", "s2", "s3", "s4")); len(got) != 0 {
		t.Errorf("got %v, want nothing from an unusable judge", got)
	}
	if calls != 1 {
		t.Errorf("requests = %d, want 1: the round ends at the first failure", calls)
	}
}
