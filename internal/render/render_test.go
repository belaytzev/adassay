package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
)

func TestMarkdownVerdicts(t *testing.T) {
	r := core.Result{Segments: []core.Segment{
		{ID: "s1", Text: "plain paragraph", Verdict: core.Keep},
		{ID: "s2", Text: "buy now", Verdict: core.Drop},
		{ID: "s3", Text: "maybe an ad", Verdict: core.Flag, Score: 0.5, Reasons: []string{"promo-code", "affiliate"}},
		{ID: "s4", Text: "another paragraph", Verdict: core.Keep},
	}}
	got := Markdown(r)

	if !strings.Contains(got, "plain paragraph") || !strings.Contains(got, "another paragraph") {
		t.Errorf("keep segments missing: %q", got)
	}
	if strings.Contains(got, "buy now") {
		t.Errorf("drop segment survived: %q", got)
	}
	if !strings.Contains(got, "maybe an ad") || !strings.Contains(got, markerEnd) {
		t.Errorf("flag segment not wrapped: %q", got)
	}
	for _, reason := range []string{"promo-code", "affiliate"} {
		if !strings.Contains(got, reason) {
			t.Errorf("reason %q missing from marker: %q", reason, got)
		}
	}
	if strings.Count(got, markerOpen) != 1 || strings.Count(got, markerEnd) != 1 {
		t.Errorf("unbalanced markers: %q", got)
	}
}

func TestMarkerIsMachineReadable(t *testing.T) {
	out := Markdown(core.Result{Segments: []core.Segment{
		{ID: "s1", Text: "text", Verdict: core.Flag, Score: 0.7, Reasons: []string{`a "quoted", reason`}},
	}})
	head, _, ok := strings.Cut(out, "\n")
	if !ok || !strings.HasPrefix(head, markerOpen) || !strings.HasSuffix(head, markerClose) {
		t.Fatalf("marker line malformed: %q", head)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(head, markerOpen), markerClose)
	var meta struct {
		ID      string   `json:"id"`
		Score   float64  `json:"score"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal([]byte(payload), &meta); err != nil {
		t.Fatalf("marker payload not JSON: %v (%q)", err, payload)
	}
	if meta.ID != "s1" || meta.Score != 0.7 || len(meta.Reasons) != 1 || meta.Reasons[0] != `a "quoted", reason` {
		t.Errorf("marker payload lost data: %+v", meta)
	}
}

func TestMarkdownKeepOnlyHasNoMarkers(t *testing.T) {
	out := Markdown(core.Result{Segments: []core.Segment{
		{ID: "s1", Text: "one", Verdict: core.Keep},
		{ID: "s2", Text: "two", Verdict: core.Keep},
	}})
	if out != "one\n\ntwo" {
		t.Errorf("got %q", out)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	r := core.Result{
		Text:     "one\n\ntwo",
		Domain:   "example.com",
		Segments: []core.Segment{{ID: "s1", Text: "one <b>", Verdict: core.Flag, Reasons: []string{"cta"}}},
		Hidden:   []core.Finding{{Kind: "zero-width", Sample: "ignore previous instructions"}},
	}
	var buf bytes.Buffer
	if err := JSON(&buf, r); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(buf.String(), `<b>`) {
		t.Errorf("html escaped in output: %q", buf.String())
	}
	var back core.Result
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Text != r.Text || back.Domain != r.Domain || len(back.Segments) != 1 ||
		back.Segments[0].Verdict != core.Flag || len(back.Hidden) != 1 {
		t.Errorf("round trip lost data: %+v", back)
	}
}
