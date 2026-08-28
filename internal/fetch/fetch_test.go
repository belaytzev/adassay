package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

func TestRefusePredicates(t *testing.T) {
	cases := []struct {
		addr       string
		refused    bool
		pubRefused bool
	}{
		{"127.0.0.1", false, true},
		{"127.1.2.3", false, true},
		{"::1", false, true},
		{"192.168.1.5", true, true},
		{"10.0.0.1", true, true},
		{"169.254.169.254", true, true},
		{"100.64.0.1", true, true},
		{"0.0.0.0", true, true},
		{"fd00::1", true, true},
		{"fe80::1", true, true},
		// 4-in-6 is what a dual-stack resolver hands the dialer.
		{"::ffff:127.0.0.1", false, true},
		{"::ffff:192.168.1.5", true, true},
		{"::ffff:100.64.0.1", true, true},
		{"::ffff:169.254.169.254", true, true},
		{"93.184.216.34", false, false},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			target := netip.AddrPortFrom(netip.MustParseAddr(c.addr), 80)
			if got := refuse(target) != nil; got != c.refused {
				t.Errorf("refuse(%s) refused = %v, want %v", c.addr, got, c.refused)
			}
			if got := refusePublic(target) != nil; got != c.pubRefused {
				t.Errorf("refusePublic(%s) refused = %v, want %v", c.addr, got, c.pubRefused)
			}
		})
	}
}

func TestGetPublicRefusesLoopbackThatGetStillFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	if _, err := Get(srv.URL); err != nil {
		t.Fatalf("Get: %v", err)
	}
	_, err := GetPublic(srv.URL)
	if err == nil {
		t.Fatal("GetPublic fetched a loopback address")
	}
	if !strings.Contains(err.Error(), "private address") {
		t.Errorf("err = %v, want the dialer refusal", err)
	}
}

// A redirect chooses its own port, so the guard every hop passes through has to
// be the one that refuses it: checking the submitted URL only covers hop one.
func TestPublicGuardRefusesNonDefaultPorts(t *testing.T) {
	public := netip.MustParseAddr("93.184.216.34")
	for _, port := range []uint16{22, 25, 6379, 8080, 11211} {
		if err := refusePublic(netip.AddrPortFrom(public, port)); err == nil {
			t.Errorf("refusePublic dialled port %d", port)
		}
	}
	for _, port := range []uint16{80, 443} {
		if err := refusePublic(netip.AddrPortFrom(public, port)); err != nil {
			t.Errorf("refusePublic(port %d) = %v, want no refusal", port, err)
		}
	}
	// The CLI talks to whatever a developer runs locally, so it keeps every port.
	if err := refuse(netip.AddrPortFrom(public, 8080)); err != nil {
		t.Errorf("refuse(port 8080) = %v, want no refusal", err)
	}
}
