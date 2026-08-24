package share

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"time"

	"adassay.com/internal/core"
	"adassay.com/internal/judge"
	"adassay.com/internal/pipeline"
	"adassay.com/internal/store"
)

const EnvOptOut = "ADASSAY_NO_SHARE"

const (
	FlushAge = 6 * time.Hour
	FlushMin = 20

	maxBatch = 256
)

type Spool interface {
	Enqueue(e core.SubmitEntry) error
	Pending() ([]core.SubmitEntry, time.Time, error)
	ClearPending(hashes []string) error
}

type Outbox struct {
	Spool  Spool
	Client *Client
	Log    *slog.Logger
	Now    func() time.Time

	Source string
}

func NewOutbox(spool Spool, c *Client, optOut bool) *Outbox {
	if spool == nil || c == nil || optOut || OptedOut() {
		return nil
	}
	return &Outbox{Spool: spool, Client: c}
}

func OptedOut() bool {
	v := os.Getenv(EnvOptOut)
	return v != "" && v != "0" && v != "false"
}

func (o *Outbox) Record(res core.Result, adopted map[string]bool) {
	if o == nil {
		return
	}
	for _, seg := range res.Segments {
		if seg.Verdict != core.Drop || adopted[seg.ID] {
			continue
		}

		if slices.Contains(seg.Reasons, pipeline.ReasonDomain) {
			continue
		}
		o.enqueue(core.SubmitEntry{
			Hash:    store.HexHash(seg.Text),
			Verdict: core.Drop,
			Reasons: seg.Reasons,
			Source:  o.sourceOf(seg),
		})
	}
	for _, f := range res.Hidden {
		o.enqueue(core.SubmitEntry{

			Hash:    store.HexHash("hidden/" + f.Kind + "\n" + f.Sample),
			Verdict: core.Drop,

			Reasons: []string{"hidden_" + f.Kind},
			Source:  o.sourceOf(core.Segment{}),
		})
	}
}

func (o *Outbox) Flush() {
	if o == nil {
		return
	}
	entries, oldest, err := o.Spool.Pending()
	if err != nil {
		o.log().Warn("outbox: read failed", "err", err)
		return
	}
	if len(entries) < FlushMin || o.now().Sub(oldest) < FlushAge {
		return
	}
	o.send(entries)
}

func (o *Outbox) FlushNow() {
	if o == nil {
		return
	}
	for last := 0; ; {
		entries, _, err := o.Spool.Pending()
		if err != nil {
			o.log().Warn("outbox: read failed", "err", err)
			return
		}
		if len(entries) == 0 || !o.send(entries) {
			return
		}
		if last > 0 && len(entries) >= last {
			o.log().Warn("outbox: spool is not draining", "pending", len(entries))
			return
		}
		last = len(entries)
	}
}

func (o *Outbox) send(entries []core.SubmitEntry) bool {
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	if len(entries) > maxBatch {
		entries = entries[:maxBatch]
	}
	if err := o.Client.Submit(entries); err != nil {
		o.log().Warn("outbox: submit failed", "entries", len(entries), "err", err)
		return false
	}
	hashes := make([]string, len(entries))
	for i, e := range entries {
		hashes[i] = e.Hash
	}
	if err := o.Spool.ClearPending(hashes); err != nil {
		o.log().Warn("outbox: clear failed", "err", err)
		return false
	}
	return true
}

func (c *Client) Submit(entries []core.SubmitEntry) error {
	id, err := c.identity()
	if err != nil {
		return fmt.Errorf("share: /v1/segments: %w", err)
	}
	return c.post("/v1/segments", id, core.SubmitRequest{
		ClientID:    id.ClientID,
		NormVersion: core.NormVersion,
		Entries:     entries,
	})
}

func (c *Client) Vote(hash string, v core.Verdict) error {
	id, err := c.identity()
	if err != nil {
		return fmt.Errorf("share: /v1/vote: %w", err)
	}
	return c.post("/v1/vote", id, core.VoteRequest{
		ClientID:    id.ClientID,
		NormVersion: core.NormVersion,
		Hash:        hash,
		Verdict:     v,
	})
}

func (c *Client) post(path string, id Identity, body any) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("share: no endpoint configured, set $%s", EnvEndpoint)
	}

	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("share: %s: %w", path, err)
	}
	req, err := id.Request(http.MethodPost, c.BaseURL+path, b)
	if err != nil {
		return fmt.Errorf("share: %s: %w", path, err)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("share: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("share: %s: %s", path, resp.Status)
	}
	return nil
}

func (o *Outbox) enqueue(e core.SubmitEntry) {
	if err := o.Spool.Enqueue(e); err != nil {
		o.log().Warn("outbox: enqueue failed", "err", err)
	}
}

func (o *Outbox) sourceOf(seg core.Segment) string {
	if o.Source != "" {
		return o.Source
	}
	if slices.Contains(seg.Reasons, judge.Reason) {
		return core.SourceOllama
	}
	return core.SourceRules
}

func (o *Outbox) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}
	return o.Now()
}

func (o *Outbox) log() *slog.Logger {
	if o.Log == nil {
		return slog.Default()
	}
	return o.Log
}
