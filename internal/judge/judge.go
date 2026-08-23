// Package judge asks a local Ollama model about the segments the deterministic
// rules could not decide. It never fails a run: an unreachable model or an
// unparseable answer leaves the grey zone exactly as it was.
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

// Reason marks a verdict the model decided, so it stays distinguishable from a
// rules verdict downstream.
const Reason = "judge"

// maxBody caps the model's answer: a verdict list for one page is kilobytes,
// and a runaway generation must not be read into memory whole.
const maxBody = 1 << 20

type Judge struct {
	Cfg  config.Judge
	HTTP *http.Client
	Log  *slog.Logger
}

func New(cfg config.Judge) *Judge {
	return &Judge{Cfg: cfg, HTTP: &http.Client{Timeout: cfg.Timeout}}
}

type request struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format"`
}

type response struct {
	Response string `json:"response"`
}

type answer struct {
	Verdicts []struct {
		ID      string `json:"id"`
		Verdict string `json:"verdict"`
	} `json:"verdicts"`
}

// Decide returns a verdict per segment id. Ids missing from the result were not
// decided — by a short answer, a broken one or no answer at all — and the
// caller keeps whatever it had.
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
			// One failure ends the round: a model that did not answer the
			// first batch will not answer the eighth, and retrying costs the
			// full timeout per batch while the reader waits.
			j.log().Warn("judge unavailable, grey zone left as is", "err", err, "segments", len(segs)-start)
			break
		}
		// Matching is by explicit id only. A model that drops, duplicates or
		// reorders entries must not shift verdicts onto neighbouring segments.
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
		Model:  j.Cfg.Model,
		Prompt: Build(topic, segs),
		Stream: false,
		Format: "json",
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(j.Cfg.Endpoint, "/") + "/api/generate"
	resp, err := j.client().Post(url, "application/json", bytes.NewReader(body))
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
	return Parse(outer.Response)
}

// Parse reads the model's own JSON payload. Unknown verdict strings are dropped
// rather than defaulted: a silent Keep would be indistinguishable from a real
// one.
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
