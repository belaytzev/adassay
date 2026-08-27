package web

import (
	"embed"
	"net/http"

	"adassay.com/internal/core"
)

//go:embed site/cases/*.html
var caseFS embed.FS

// The address each fixture stands in for. Nothing is fetched: the map is also
// the list of slugs the demo answers for.
var caseURLs = map[string]string{
	"keyboards": "https://example.com/seven-mechanical-keyboards-for-programmers",
	"injection": "https://example.com/choosing-a-static-site-generator",
}

type demoCase struct {
	res  core.Result
	code string
}

// newCases runs every fixture through Analyze once, at startup. A case that is
// derived rather than committed as JSON cannot go stale when the rules change.
func newCases(a *Analyzer) map[string]demoCase {
	cases := make(map[string]demoCase, len(caseURLs))
	for slug, pageURL := range caseURLs {
		page, err := caseFS.ReadFile("site/cases/" + slug + ".html")
		if err != nil {
			cases[slug] = demoCase{code: "failed"}
			continue
		}
		fixture := &Analyzer{Cfg: a.Cfg, Fetch: func(string) ([]byte, error) { return page, nil }}
		res, err := fixture.Analyze(pageURL)
		code := ""
		if err != nil {
			code = Code(err)
		}
		cases[slug] = demoCase{res: res, code: code}
	}
	return cases
}

func handleCase(w http.ResponseWriter, r *http.Request, cases map[string]demoCase) {
	c, ok := cases[r.PathValue("slug")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	write(w, c.res, c.code)
}
