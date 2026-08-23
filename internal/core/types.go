package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Verdict int

const (
	Keep Verdict = iota
	Flag
	Drop
)

const (
	SourceRules  = "rules"
	SourceOllama = "ollama"
	SourceShared = "shared"
	SourceHuman  = "human"
)

var verdictNames = map[Verdict]string{
	Keep: "keep",
	Flag: "flag",
	Drop: "drop",
}

var verdictValues = map[string]Verdict{
	"keep": Keep,
	"flag": Flag,
	"drop": Drop,
}

func (v Verdict) String() string {
	if s, ok := verdictNames[v]; ok {
		return s
	}
	return fmt.Sprintf("Verdict(%d)", int(v))
}

func (v Verdict) MarshalJSON() ([]byte, error) {
	s, ok := verdictNames[v]
	if !ok {
		return nil, fmt.Errorf("core: unknown verdict %d", int(v))
	}
	return json.Marshal(s)
}

func (v *Verdict) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("core: verdict must be a string: %w", err)
	}
	parsed, ok := verdictValues[s]
	if !ok {
		return fmt.Errorf("core: unknown verdict %q", s)
	}
	*v = parsed
	return nil
}

func ParseVerdict(s string) (Verdict, error) {
	v, ok := verdictValues[s]
	if !ok {
		return Keep, fmt.Errorf("core: unknown verdict %q", s)
	}
	return v, nil
}

const MaxReason = 48

func ValidReason(r string) bool {
	return r != "" && len(r) <= MaxReason &&
		strings.Trim(r, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}

func ValidSource(s string) bool {
	switch s {
	case SourceRules, SourceOllama, SourceShared, SourceHuman:
		return true
	}
	return false
}

type Segment struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Score   float64  `json:"score"`
	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
	Links   []Link   `json:"links,omitempty"`
}

type Finding struct {
	Kind   string `json:"kind"`
	Sample string `json:"sample"`
}

type Result struct {
	Title       string    `json:"title,omitempty"`
	Text        string    `json:"text"`
	Segments    []Segment `json:"segments"`
	Hidden      []Finding `json:"hidden,omitempty"`
	SourceScore float64   `json:"source_score"`
	Domain      string    `json:"domain"`
	Visible     int       `json:"visible"`
	Thin        bool      `json:"thin,omitempty"`
}

type Link struct {
	Href string `json:"href"`
	Rel  string `json:"rel,omitempty"`
	Text string `json:"text,omitempty"`
}
