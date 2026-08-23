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

	// maxFailures stops asking after this many consecutive failed lookups. A
	// page is hundreds of segments and each lookup is its own request, so an
	// endpoint that hangs would otherwise cost timeout × segments before the
	// document is printed.
	maxFailures = 3
)

type Client struct {
	BaseURL string
	ID      string
	HTTP    *http.Client
	Log     *slog.Logger

	failures int
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
	// An id that could not be stored is worse than none: the next run would
	// generate another one, and one install voting under a fresh identity every
	// time reaches the backend's quorum by itself.
	id := uuid.NewString()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("share: client id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("share: client id: %w", err)
	}
	return id, nil
}

// Lookup asks for the bucket of a segment hash and returns the entry about
// that exact hash, if the bucket holds one. Like the judge, it never fails a
// run: an unreachable or misbehaving backend is a miss.
// ponytail: one request per segment, batch the page's prefixes into one request when the backend can answer several
func (c *Client) Lookup(hash []byte) (core.BucketEntry, bool) {
	if c == nil || c.BaseURL == "" || len(hash) == 0 || c.failures >= maxFailures {
		return core.BucketEntry{}, false
	}
	prefix := store.Prefix(hash)
	url := fmt.Sprintf("%s/v1/segments/%s?norm_version=%d", c.BaseURL, prefix, core.NormVersion)

	resp, err := c.client().Get(url)
	if err != nil {
		c.failed("share: lookup failed", "prefix", prefix, "err", err)
		return core.BucketEntry{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.failed("share: lookup refused", "prefix", prefix, "status", resp.StatusCode)
		return core.BucketEntry{}, false
	}

	var bucket core.BucketResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&bucket); err != nil {
		c.failed("share: bucket unreadable", "prefix", prefix, "err", err)
		return core.BucketEntry{}, false
	}
	c.failures = 0
	return match(hash, bucket)
}

// failed logs one unusable answer and counts it. A backend that fails three
// times in a row is down, not busy: the rest of the run stays local.
func (c *Client) failed(msg string, args ...any) {
	c.failures++
	if c.failures >= maxFailures {
		args = append(args, "giving_up", true)
	}
	c.log().Warn(msg, args...)
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
			// The reasons get written to the local database and handed to the
			// agent. Our own backend refuses anything but rule identifiers on
			// write, but the url in the config is somebody else's server.
			e.Reasons = validReasons(e.Reasons)
			return e, true
		}
	}
	return core.BucketEntry{}, false
}

func validReasons(reasons []string) []string {
	out := reasons[:0:0]
	for _, r := range reasons {
		if core.ValidReason(r) {
			out = append(out, r)
		}
	}
	return out
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
