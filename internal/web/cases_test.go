package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"adassay.com/internal/core"
)

func getCase(t *testing.T, mux http.Handler, slug string) (*httptest.ResponseRecorder, response) {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/case/"+slug, nil))

	var res response
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
	}
	return w, res
}

// The shop window has to keep showing all three verdicts. A rules change that
// empties it fails here rather than in front of a visitor.
func TestCases(t *testing.T) {
	mux := testMux(t, func(string) ([]byte, error) {
		t.Error("a saved case fetched the network")
		return nil, nil
	})

	cases := []struct {
		slug                     string
		keep, flag, drop, hidden int
	}{
		{slug: "keyboards", keep: 3, flag: 2, drop: 1},
		{slug: "injection", keep: 4, hidden: 1},
	}
	if len(cases) != len(caseURLs) {
		t.Fatalf("the table covers %d cases, want all %d", len(cases), len(caseURLs))
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			w, res := getCase(t, mux, tc.slug)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if res.Error != "" {
				t.Fatalf("error = %q, want none", res.Error)
			}

			counts := map[core.Verdict]int{}
			for _, s := range res.Segments {
				counts[s.Verdict]++
			}
			if counts[core.Keep] != tc.keep || counts[core.Flag] != tc.flag || counts[core.Drop] != tc.drop {
				t.Errorf("keep/flag/drop = %d/%d/%d, want %d/%d/%d",
					counts[core.Keep], counts[core.Flag], counts[core.Drop], tc.keep, tc.flag, tc.drop)
			}
			if len(res.Hidden) != tc.hidden {
				t.Errorf("hidden = %d, want %d", len(res.Hidden), tc.hidden)
			}
			if res.Text == "" {
				t.Error("text is empty")
			}
		})
	}
}

func TestUnknownCaseIs404(t *testing.T) {
	mux := testMux(t, nil)
	if w, _ := getCase(t, mux, "nope"); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
