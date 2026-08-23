package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateAndNonHTTPTargetsAreRefused(t *testing.T) {
	cases := map[string]string{
		"file scheme":      "file:///etc/passwd",
		"private address":  "http://10.0.0.1/",
		"link local":       "http://169.254.169.254/latest/meta-data/",
		"private hostname": "http://192.168.0.1:8080/page",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Get(target); err == nil {
				t.Fatalf("Get(%q) succeeded, want a refusal", target)
			}
		})
	}
}

func TestRedirectToPrivateAddressIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	_, err := Get(srv.URL)
	if err == nil {
		t.Fatal("a redirect into link-local space was followed")
	}
	if !strings.Contains(err.Error(), "private address") {
		t.Errorf("err = %v, want the dialer refusal", err)
	}
}

func TestRequestLooksLikeABrowser(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	if _, err := Get(srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ua := got.Get("User-Agent"); !strings.Contains(ua, "Mozilla/5.0") || !strings.Contains(ua, "Chrome/") {
		t.Errorf("User-Agent = %q, want a browser one", ua)
	}
	for _, name := range []string{"Accept", "Accept-Language", "Upgrade-Insecure-Requests", "Sec-Fetch-Mode"} {
		if got.Get(name) == "" {
			t.Errorf("%s was not sent", name)
		}
	}
	if !strings.Contains(got.Get("Accept"), "text/html") {
		t.Errorf("Accept = %q, want html in it", got.Get("Accept"))
	}
}

func TestBlockIsDistinguishableFromOtherFailures(t *testing.T) {
	cases := []struct {
		name    string
		code    int
		header  string
		blocked bool
	}{
		{"forbidden", http.StatusForbidden, "", true},
		{"rate limited", http.StatusTooManyRequests, "", true},
		{"cloudflare challenge", http.StatusServiceUnavailable, "challenge", true},
		{"not found", http.StatusNotFound, "", false},
		{"server error", http.StatusInternalServerError, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.header != "" {
					w.Header().Set("cf-mitigated", c.header)
				}
				w.WriteHeader(c.code)
			}))
			defer srv.Close()

			_, err := Get(srv.URL)
			if err == nil {
				t.Fatalf("Get returned no error on %d", c.code)
			}
			if errors.Is(err, ErrBlocked) != c.blocked {
				t.Errorf("errors.Is(%v, ErrBlocked) = %v, want %v", err, !c.blocked, c.blocked)
			}
		})
	}
}

func TestEmptyPageIsNotABlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	page, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(page) != 0 {
		t.Errorf("page = %q, want empty", page)
	}
}
