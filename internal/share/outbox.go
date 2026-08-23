package share

import (
	"bytes"
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

// EnvOptOut disables submission entirely; the --no-share flag does the same.
const EnvOptOut = "ADASSAY_NO_SHARE"

const (
	// FlushAge and FlushMin are both required before anything is sent: a batch
	// that leaves immediately, or a batch of one, hands the backend a reading
	// session in real time.
	FlushAge = 6 * time.Hour
	FlushMin = 20

	// maxBatch mirrors the cap the backend enforces on POST /v1/segments.
	maxBatch = 256
)

// Spool is the disk side of the outbox. It is an interface only so the flush
// policy can be tested without sqlite.
type Spool interface {
	Enqueue(e core.SubmitEntry) error
	Pending() ([]core.SubmitEntry, time.Time, error)
	ClearPending(hashes []string) error
}

// Outbox queues confident verdicts on disk and sends them on a later run. A
// nil Outbox is an opted-out install: every method is a no-op.
type Outbox struct {
	Spool  Spool
	Client *Client
	Log    *slog.Logger
	Now    func() time.Time
}

// NewOutbox returns nil when sharing is off or unconfigured, so the caller has
// nothing to remember beyond calling Record and Flush.
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

// Record queues what the run is sure about: dropped segments and hidden-text
// findings. The grey zone stays home — a Flag is an open question, and sending
// one would leak a page without contributing a verdict. Verdicts listed in
// adopted are skipped: they were read from the shared database or from a
// cached authority, and echoing them back is a vote for nothing.
func (o *Outbox) Record(res core.Result, adopted map[string]bool) {
	if o == nil {
		return
	}
	for _, seg := range res.Segments {
		if seg.Verdict != core.Drop || adopted[seg.ID] {
			continue
		}
		// A Drop the domain score pushed up is a claim about this domain's
		// reputation here, not about the text: the same paragraph elsewhere
		// scored in the grey zone. Sending it would turn one install's
		// distrust into a global verdict on words that never earned it.
		if slices.Contains(seg.Reasons, pipeline.ReasonDomain) {
			continue
		}
		o.enqueue(core.SubmitEntry{
			Hash:    store.HexHash(seg.Text),
			Verdict: core.Drop,
			Reasons: seg.Reasons,
			Source:  source(seg),
		})
	}
	for _, f := range res.Hidden {
		o.enqueue(core.SubmitEntry{
			// Hidden samples live in their own hash space. A sample is often
			// ordinary page prose — a meta description, an infobox caption —
			// and L1 fires on clean pages, so hashing it like a segment would
			// publish a Drop that silences that same paragraph everywhere it
			// is visible.
			Hash:    store.HexHash("hidden/" + f.Kind + "\n" + f.Sample),
			Verdict: core.Drop,
			// Underscore, not a colon: the backend accepts reasons as rule
			// identifiers only, and a punctuated one is dropped on arrival.
			Reasons: []string{"hidden_" + f.Kind},
			Source:  core.SourceRules,
		})
	}
}

// Flush sends the spool if it is both old enough and large enough. The order is
// shuffled first: rows arrive in reading order, and reading order is exactly
// what the batching is meant to hide.
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
	rand.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
	if len(entries) > maxBatch {
		entries = entries[:maxBatch]
	}
	if err := o.Client.Submit(entries); err != nil {
		o.log().Warn("outbox: submit failed", "entries", len(entries), "err", err)
		return
	}
	hashes := make([]string, len(entries))
	for i, e := range entries {
		hashes[i] = e.Hash
	}
	if err := o.Spool.ClearPending(hashes); err != nil {
		o.log().Warn("outbox: clear failed", "err", err)
	}
}

// Submit posts a batch of verdicts to the shared database.
func (c *Client) Submit(entries []core.SubmitEntry) error {
	return c.post("/v1/segments", core.SubmitRequest{
		ClientID:    c.ID,
		NormVersion: core.NormVersion,
		Entries:     entries,
	})
}

// Vote is the human override: it carries a full hash, because the point of a
// vote is to correct one exact segment.
func (c *Client) Vote(hash string, v core.Verdict) error {
	return c.post("/v1/vote", core.VoteRequest{
		ClientID:    c.ID,
		NormVersion: core.NormVersion,
		Hash:        hash,
		Verdict:     v,
	})
}

func (c *Client) post(path string, body any) error {
	if c == nil || c.BaseURL == "" {
		return fmt.Errorf("share: no endpoint configured, set $%s", EnvEndpoint)
	}
	// Reads need no identity, writes do: without a stored id every batch would
	// count as a different installation towards the backend's quorum.
	if c.ID == "" {
		return fmt.Errorf("share: %s: no client id, the config directory is not writable", path)
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("share: %s: %w", path, err)
	}
	resp, err := c.client().Post(c.BaseURL+path, "application/json", bytes.NewReader(b))
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

func source(seg core.Segment) string {
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
