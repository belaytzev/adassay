// Package share reads verdicts from the shared database. A lookup is
// k-anonymous: only the leading core.PrefixLen hex characters of a hash leave
// the machine, and the bucket that comes back is matched against the full hash
// here. The backend therefore learns a bucket, never a segment.
package share

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/store"
)

// EnvEndpoint points the client at a shared database. Unset means no client at
// all: nothing is looked up and nothing leaves the machine.
const EnvEndpoint = "ADFILTER_SHARE_URL"

const (
	timeout = 5 * time.Second
	maxBody = 1 << 20
)

type Client struct {
	BaseURL string
	ID      string
	HTTP    *http.Client
	Log     *slog.Logger
}

// New returns nil when no endpoint is configured, so an unconfigured install
// is an absent client rather than one that fails every call.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = os.Getenv(EnvEndpoint)
	}
	if baseURL == "" {
		return nil
	}
	id, err := ClientID()
	if err != nil {
		slog.Default().Warn("share: client id", "err", err)
	}
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		ID:      id,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// IDPath is where the installation identifier lives. It is configuration, not
// a cache: regenerating it would reset the reputation the backend weighs
// submissions by.
func IDPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("share: no config directory: %w", err)
	}
	return filepath.Join(dir, "adfilter", "client_id"), nil
}

// ClientID returns the random identifier of this installation, creating it on
// first use. It is tied to nothing else: the backend needs a stable handle to
// weigh submissions and to rate-limit abuse, and reads never carry it.
func ClientID() (string, error) {
	path, err := IDPath()
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}
	id := uuid.NewString()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return id, fmt.Errorf("share: client id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return id, fmt.Errorf("share: client id: %w", err)
	}
	return id, nil
}

// Lookup asks for the bucket of a segment hash and returns the entry about
// that exact hash, if the bucket holds one. Like the judge, it never fails a
// run: an unreachable or misbehaving backend is a miss.
func (c *Client) Lookup(hash []byte) (core.BucketEntry, bool) {
	if c == nil || c.BaseURL == "" || len(hash) == 0 {
		return core.BucketEntry{}, false
	}
	prefix := store.Prefix(hash)
	url := fmt.Sprintf("%s/v1/segments/%s?norm_version=%d", c.BaseURL, prefix, core.NormVersion)

	resp, err := c.client().Get(url)
	if err != nil {
		c.log().Warn("share: lookup failed", "prefix", prefix, "err", err)
		return core.BucketEntry{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.log().Warn("share: lookup refused", "prefix", prefix, "status", resp.StatusCode)
		return core.BucketEntry{}, false
	}

	var bucket core.BucketResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&bucket); err != nil {
		c.log().Warn("share: bucket unreadable", "prefix", prefix, "err", err)
		return core.BucketEntry{}, false
	}
	return match(hash, bucket)
}

// match picks out the entry about our hash. Everything else in the bucket is a
// decoy or another client's segment and is dropped. A bucket shorter than
// core.MinBucket means the server did not pad it, which makes the lookup
// identifying: refuse the whole answer instead of taking a verdict for it.
func match(hash []byte, bucket core.BucketResponse) (core.BucketEntry, bool) {
	if bucket.NormVersion != core.NormVersion || len(bucket.Entries) < core.MinBucket {
		return core.BucketEntry{}, false
	}
	want := hex.EncodeToString(hash)
	for _, e := range bucket.Entries {
		if e.Hash == want && core.ValidSource(e.Source) {
			return e, true
		}
	}
	return core.BucketEntry{}, false
}

func (c *Client) client() *http.Client {
	if c.HTTP == nil {
		return &http.Client{Timeout: timeout}
	}
	return c.HTTP
}

func (c *Client) log() *slog.Logger {
	if c.Log == nil {
		return slog.Default()
	}
	return c.Log
}
