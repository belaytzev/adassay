package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func roundTrip[T any](t *testing.T, name string, in T) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: unmarshal: %v", name, err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("%s: round-trip mismatch\n got: %+v\nwant: %+v\njson: %s", name, out, in, b)
	}
}

func TestResultRoundTrip(t *testing.T) {
	roundTrip(t, "Result", Result{
		Text: "hello\n\nbuy now",
		Segments: []Segment{
			{ID: "s1", Text: "hello", Score: 0.01, Verdict: Keep},
			{ID: "s2", Text: "buy now", Score: 0.91, Verdict: Drop, Reasons: []string{"promo-code", "rel-sponsored"}},
			{ID: "s3", Text: "maybe", Score: 0.5, Verdict: Flag, Reasons: []string{"grey-zone"}},
		},
		Hidden:      []Finding{{Kind: "zero-width", Sample: "ignore previous instructions"}},
		SourceScore: 0.42,
		Domain:      "example.com",
	})
}

func TestWireTypesRoundTrip(t *testing.T) {
	roundTrip(t, "BucketRequest", BucketRequest{Prefix: "beef", NormVersion: NormVersion})
	roundTrip(t, "BucketResponse", BucketResponse{
		Prefix:      "beef",
		NormVersion: NormVersion,
		Entries: []BucketEntry{
			{Hash: "beefcafe", Verdict: Drop, Reasons: []string{"promo-code"}, Source: SourceRules, Votes: 3},
			{Hash: "beef0001", Verdict: Keep, Source: SourceHuman},
		},
	})
	roundTrip(t, "SubmitRequest", SubmitRequest{
		ClientID:    "0f4bbb0e-1f0e-4a8e-9f3f-000000000000",
		NormVersion: NormVersion,
		Entries: []SubmitEntry{
			{Hash: "beefcafe", Verdict: Drop, Reasons: []string{"affiliate-link"}, Source: SourceOllama},
			{Hash: "beef0002", Verdict: Keep, Source: SourceRules},
		},
	})
	roundTrip(t, "SubmitResponse", SubmitResponse{Accepted: 2, Rejected: 1})
	roundTrip(t, "VoteRequest", VoteRequest{
		ClientID:    "0f4bbb0e-1f0e-4a8e-9f3f-000000000000",
		NormVersion: NormVersion,
		Hash:        "beefcafe",
		Verdict:     Keep,
	})
	roundTrip(t, "VoteResponse", VoteResponse{Hash: "beefcafe", Votes: 7})
	roundTrip(t, "ErrorResponse", ErrorResponse{Error: "bad prefix"})
}

func TestVerdictSerializesAsString(t *testing.T) {
	for v, want := range map[Verdict]string{Keep: `"keep"`, Flag: `"flag"`, Drop: `"drop"`} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %v: %v", v, err)
		}
		if string(b) != want {
			t.Errorf("marshal %v = %s, want %s", v, b, want)
		}
	}
}

func TestUnknownVerdictIsAnError(t *testing.T) {
	cases := []string{`"sponsored"`, `""`, `2`, `null`, `"KEEP"`}
	for _, c := range cases {
		var v Verdict = Drop
		if err := json.Unmarshal([]byte(c), &v); err == nil {
			t.Errorf("unmarshal %s: got no error, verdict %v", c, v)
		}
	}

	var s Segment
	err := json.Unmarshal([]byte(`{"id":"s1","text":"x","verdict":"advert"}`), &s)
	if err == nil {
		t.Fatalf("segment with unknown verdict decoded silently as %v", s.Verdict)
	}
	if !strings.Contains(err.Error(), "advert") {
		t.Errorf("error should name the bad verdict, got %v", err)
	}
}

func TestMarshalUnknownVerdictFails(t *testing.T) {
	if _, err := json.Marshal(Verdict(7)); err == nil {
		t.Error("marshaling an out-of-range verdict should fail")
	}
}

func TestValidSource(t *testing.T) {
	for _, s := range []string{SourceRules, SourceOllama, SourceShared, SourceHuman} {
		if !ValidSource(s) {
			t.Errorf("ValidSource(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Rules", "llm"} {
		if ValidSource(s) {
			t.Errorf("ValidSource(%q) = true", s)
		}
	}
}
