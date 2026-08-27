package judge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
)

const Reason = "judge"

const maxBody = 1 << 20

type Judge struct {
	Cfg  config.Judge
	HTTP *http.Client
	Log  *slog.Logger
}

func New(cfg config.Judge) *Judge {
	return &Judge{Cfg: cfg, HTTP: &http.Client{Timeout: cfg.Timeout}}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model          string    `json:"model"`
	Messages       []message `json:"messages"`
	Stream         bool      `json:"stream"`
	Temperature    float64   `json:"temperature"`
	ResponseFormat any       `json:"response_format,omitempty"`
}

type response struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

func verdictSchema() any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "verdicts",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdicts": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":      map[string]any{"type": "string"},
								"verdict": map[string]any{"type": "string", "enum": []string{"keep", "flag", "drop"}},
							},
							"required":             []string{"id", "verdict"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"verdicts"},
				"additionalProperties": false,
			},
		},
	}
}

type answer struct {
	Verdicts []struct {
		ID      string `json:"id"`
		Verdict string `json:"verdict"`
	} `json:"verdicts"`
}

func (j *Judge) Decide(topic string, segs []core.Segment) map[string]core.Verdict {
	out := make(map[string]core.Verdict, len(segs))
	size := j.Cfg.BatchSize
	if size <= 0 {
		size = 1
	}
	for start := 0; start < len(segs); start += size {
		batch := segs[start:min(start+size, len(segs))]
		got, err := j.ask(topic, batch)
		if err != nil {

			j.log().Warn("judge unavailable, grey zone left as is", "err", err, "segments", len(segs)-start)
			break
		}

		want := make(map[string]bool, len(batch))
		for _, s := range batch {
			want[s.ID] = true
		}
		for id, v := range got {
			if want[id] {
				out[id] = v
			}
		}
	}
	return out
}

func (j *Judge) ask(topic string, segs []core.Segment) (map[string]core.Verdict, error) {
	body, err := json.Marshal(request{
		Model:          j.Cfg.Model,
		Messages:       []message{{Role: "user", Content: Build(topic, segs)}},
		Stream:         false,
		Temperature:    0,
		ResponseFormat: verdictSchema(),
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(j.Cfg.Endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// A local runtime needs no key; a gateway refuses the request without one.
	if j.Cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+j.Cfg.APIKey)
	}
	resp, err := j.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("judge: %s: %s", url, resp.Status)
	}
	var outer response
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&outer); err != nil {
		return nil, fmt.Errorf("judge: decode envelope: %w", err)
	}
	if len(outer.Choices) == 0 {
		return nil, fmt.Errorf("judge: %s: no choices in response", url)
	}
	return Parse(outer.Choices[0].Message.Content)
}

func Parse(payload string) (map[string]core.Verdict, error) {
	var a answer
	if err := json.Unmarshal([]byte(payload), &a); err != nil {
		return nil, fmt.Errorf("judge: decode answer: %w", err)
	}
	out := make(map[string]core.Verdict, len(a.Verdicts))
	for _, v := range a.Verdicts {
		parsed, err := core.ParseVerdict(v.Verdict)
		if err != nil {
			continue
		}
		out[v.ID] = parsed
	}
	return out, nil
}

func (j *Judge) client() *http.Client {
	if j.HTTP == nil {
		return &http.Client{Timeout: j.Cfg.Timeout}
	}
	return j.HTTP
}

func (j *Judge) log() *slog.Logger {
	if j.Log == nil {
		return slog.Default()
	}
	return j.Log
}
