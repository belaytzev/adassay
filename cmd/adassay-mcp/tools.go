package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/fetch"
	"adassay.com/internal/pipeline"
	"adassay.com/internal/render"
	"adassay.com/internal/store"
)

const version = "0.1.0"

type urlArgs struct {
	URL string `json:"url" jsonschema:"the page to fetch and filter"`
}

type textArgs struct {
	Text string `json:"text" jsonschema:"the text to check: plain text or an HTML fragment"`
}

// report is what the agent judges on. The verdicts are an opinion, so the
// evidence behind them travels with the document: a domain caught serving
// hidden text is worth knowing about even when nothing was dropped.
type report struct {
	Markdown    string         `json:"markdown" jsonschema:"the document with ads cut and grey-zone segments wrapped in markers"`
	Title       string         `json:"title,omitempty"`
	Domain      string         `json:"domain,omitempty"`
	SourceScore float64        `json:"source_score" jsonschema:"trust in the domain from 0 to 1; 1 means nothing is held against it"`
	HiddenCount int            `json:"hidden_count" jsonschema:"segments served to parsers but hidden from people"`
	Hidden      []core.Finding `json:"hidden,omitempty"`
	Dropped     int            `json:"dropped"`
	Flagged     int            `json:"flagged"`
}

type server struct {
	cfg   *config.Config
	db    *store.Store
	judge pipeline.Judge
}

func newServer(s *server) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "adassay", Version: version}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fetch_clean",
		Description: "Fetch a web page and return its main content with advertising and marketing inserts removed, plus what was found on the way.",
	}, s.fetchClean)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "check_text",
		Description: "Check text you already have for advertising and marketing inserts. Fetches nothing: the text is never sent anywhere except, for segments the rules cannot decide, to the model at the configured judge endpoint (a local Ollama by default).",
	}, s.checkText)
	return srv
}

func (s *server) fetchClean(_ context.Context, _ *mcp.CallToolRequest, in urlArgs) (*mcp.CallToolResult, report, error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, report{}, fmt.Errorf("adassay: url is empty")
	}
	page, err := fetch.Get(in.URL)
	if err != nil {
		return nil, report{}, err
	}
	return s.analyze(page, in.URL)
}

func (s *server) checkText(_ context.Context, _ *mcp.CallToolRequest, in textArgs) (*mcp.CallToolResult, report, error) {
	return s.analyze(wrap(in.Text), "")
}

func (s *server) analyze(page []byte, pageURL string) (*mcp.CallToolResult, report, error) {
	res, err := extract.Extract(page, pageURL, s.cfg.L1)
	if err != nil {
		return nil, report{}, err
	}
	if res.Domain != "" {
		if err := s.db.Visit(res.Domain, len(res.Hidden)); err != nil {
			return nil, report{}, err
		}
	}
	res, err = (&pipeline.Pipeline{Cfg: s.cfg, Cache: s.db, Judge: s.judge}).Run(res)
	if err != nil {
		return nil, report{}, err
	}

	// The structured half of the result is read by the same agent that reads
	// the markdown, so the title and the hidden samples — page-controlled text,
	// the samples being the injections themselves — get defused as well.
	rep := report{Markdown: render.Markdown(res)}
	res = render.Safe(res)
	rep.Title = res.Title
	rep.Domain = res.Domain
	rep.SourceScore = res.SourceScore
	rep.HiddenCount = len(res.Hidden)
	rep.Hidden = res.Hidden
	for _, seg := range res.Segments {
		switch seg.Verdict {
		case core.Drop:
			rep.Dropped++
		case core.Flag:
			rep.Flagged++
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: rep.Markdown}}}, rep, nil
}

// wrap turns the caller's text into a document the extractor understands. Blank
// lines are the paragraph breaks, and markup is left as it came: escaping it
// would strip the rel and href attributes the L2 rules read.
func wrap(text string) []byte {
	var b strings.Builder
	b.WriteString("<html><body><article>")
	for _, block := range strings.Split(text, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(block)
		b.WriteString("</p>")
	}
	b.WriteString("</article></body></html>")
	return []byte(b.String())
}
