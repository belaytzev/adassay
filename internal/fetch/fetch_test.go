package fetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The url can come from an agent acting on a page it just read, so a fetch must
// not become a probe of the network the machine sits on.
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

// A redirect is the other half of the same check: an allowed url may point at
// a private address only after the first hop.
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
