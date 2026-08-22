// Package fetch retrieves a page over HTTP for both binaries, so the timeout,
// the body cap and the User-Agent are decided in one place.
package fetch

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	Timeout   = 20 * time.Second
	UserAgent = "adfilter/0.1 (+https://github.com/belaytzev/adfilter)"
	MaxBody   = 8 << 20
)

func Get(pageURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("adfilter: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := (&http.Client{Timeout: Timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("adfilter: fetch %s: %s", pageURL, resp.Status)
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return nil, fmt.Errorf("adfilter: fetch %s: %w", pageURL, err)
	}
	return page, nil
}
