package share

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"adassay.com/internal/core"
)

const (
	HeaderInstall = "X-Adassay-Install"
	HeaderSecret  = "X-Adassay-Secret"
)

type Identity struct {
	ClientID string `json:"client_id"`
	Secret   string `json:"secret"`
}

func CredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("share: no config directory: %w", err)
	}
	return filepath.Join(dir, "adassay", "credentials.json"), nil
}

func LoadIdentity() (Identity, bool) {
	path, err := CredentialsPath()
	if err != nil {
		return Identity{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, false
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil || id.ClientID == "" || id.Secret == "" {
		return Identity{}, false
	}
	return id, true
}

func Register(baseURL string, hc *http.Client) (Identity, error) {
	resp, err := hc.Post(baseURL+"/v1/register", "application/json", nil)
	if err != nil {
		return Identity{}, fmt.Errorf("share: register: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("share: register: %s", resp.Status)
	}
	var out core.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Identity{}, fmt.Errorf("share: register: %w", err)
	}
	if out.ClientID == "" || out.Secret == "" {
		return Identity{}, fmt.Errorf("share: register: empty credentials")
	}
	id := Identity{ClientID: out.ClientID, Secret: out.Secret}
	if err := id.save(); err != nil {
		return Identity{}, err
	}
	return id, nil
}

func (i Identity) save() error {
	path, err := CredentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("share: credentials: %w", err)
	}
	b, err := json.Marshal(i)
	if err != nil {
		return fmt.Errorf("share: credentials: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("share: credentials: %w", err)
	}
	return nil
}

func (i Identity) Request(method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderInstall, i.ClientID)
	req.Header.Set(HeaderSecret, i.Secret)
	return req, nil
}

func (c *Client) identity() (Identity, error) {
	if c.Ident.ClientID != "" {
		return c.Ident, nil
	}
	if id, ok := LoadIdentity(); ok {
		c.Ident = id
		return id, nil
	}
	id, err := Register(c.BaseURL, c.client())
	if err != nil {
		return Identity{}, err
	}
	c.Ident = id
	return id, nil
}
