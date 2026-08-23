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

	"adassay.com/internal/core"
	"adassay.com/internal/store"
)

const EnvEndpoint = "ADASSAY_SHARE_URL"

const (
	timeout = 5 * time.Second
	maxBody = 1 << 20

	maxFailures = 3
)

type Client struct {
	BaseURL string
	ID      string
	HTTP    *http.Client
	Log     *slog.Logger

	failures int
}

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

func IDPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("share: no config directory: %w", err)
	}
	return filepath.Join(dir, "adassay", "client_id"), nil
}

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
		return "", fmt.Errorf("share: client id: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("share: client id: %w", err)
	}
	return id, nil
}

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

func (c *Client) failed(msg string, args ...any) {
	c.failures++
	if c.failures >= maxFailures {
		args = append(args, "giving_up", true)
	}
	c.log().Warn(msg, args...)
}

func match(hash []byte, bucket core.BucketResponse) (core.BucketEntry, bool) {
	if bucket.NormVersion != core.NormVersion || len(bucket.Entries) < core.MinBucket {
		return core.BucketEntry{}, false
	}
	want := hex.EncodeToString(hash)
	for _, e := range bucket.Entries {
		if e.Hash == want && core.ValidSource(e.Source) {

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
