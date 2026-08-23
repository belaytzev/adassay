package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"adassay.com/internal/config"
	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/rules"
)

const labelsFile = "labels.yaml"

type corpusLabels struct {
	Pages []pageLabel `yaml:"pages"`
}

type pageLabel struct {
	File   string   `yaml:"file"`
	URL    string   `yaml:"url,omitempty"`
	Hidden bool     `yaml:"hidden,omitempty"`
	Ads    []string `yaml:"ads,omitempty"`
	Native []string `yaml:"native,omitempty"`
}

type scored struct {
	page     string
	id       string
	text     string
	features []string
	score    float64
	forced   core.Verdict
	shortcut bool
	ad       bool
	native   bool
}

func (s scored) commercial() bool { return s.ad || s.native }

func (s scored) verdict(l2 config.L2) core.Verdict {
	if s.shortcut {
		return s.forced
	}
	return rules.Classify(s.score, l2)
}

type pageEval struct {
	label  pageLabel
	hidden bool
}

type evaluation struct {
	segments []scored
	pages    []pageEval
}

type metrics struct {
	hi, lo         float64
	tp, fp, fn     int
	ads            int
	covered, noise int
	nativeTotal    int
	nativeCaught   int
	segments       int
	flags          int
}

func (m metrics) coverage() float64 {
	if m.ads == 0 {
		return 0
	}
	return float64(m.covered) / float64(m.ads)
}

func (m metrics) nativeCoverage() float64 {
	if m.nativeTotal == 0 {
		return 0
	}
	return float64(m.nativeCaught) / float64(m.nativeTotal)
}

func (m metrics) flagRate() float64 {
	if m.segments == 0 {
		return 0
	}
	return float64(m.flags) / float64(m.segments)
}

func (m metrics) precision() float64 {
	if m.tp+m.fp == 0 {
		return 0
	}
	return float64(m.tp) / float64(m.tp+m.fp)
}

func (m metrics) recall() float64 {
	if m.tp+m.fn == 0 {
		return 0
	}
	return float64(m.tp) / float64(m.tp+m.fn)
}

func calibrate(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("adassay calibrate", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: adassay calibrate [flags]\n\nScores the labelled corpus on a grid of l2.hi and l2.lo.\n\nFlags:")
		fs.PrintDefaults()
	}
	dir := fs.String("corpus", "testdata/corpus", "directory holding the corpus and "+labelsFile)
	cfgPath := fs.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dump := fs.Bool("dump", false, "print every segment with its verdict instead of the grid, to write labels from")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ev, err := evalCorpus(*dir, cfg)
	if err != nil {
		return err
	}
	if *dump {
		return dumpSegments(stdout, ev, cfg.L2)
	}
	return report(stdout, ev, cfg.L2)
}

func evalCorpus(dir string, cfg *config.Config) (evaluation, error) {
	var ev evaluation
	data, err := os.ReadFile(filepath.Join(dir, labelsFile))
	if err != nil {
		return ev, fmt.Errorf("calibrate: %w", err)
	}
	var labels corpusLabels
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&labels); err != nil {
		return ev, fmt.Errorf("calibrate: %s: %w", labelsFile, err)
	}
	if len(labels.Pages) == 0 {
		return ev, fmt.Errorf("calibrate: %s has no pages", labelsFile)
	}

	for _, p := range labels.Pages {
		page, err := os.ReadFile(filepath.Join(dir, p.File))
		if err != nil {
			return ev, fmt.Errorf("calibrate: %w", err)
		}
		res, err := extract.Extract(page, p.URL, cfg.L1)
		if err != nil {
			return ev, fmt.Errorf("calibrate: %s: %w", p.File, err)
		}
		matched := make([]bool, len(p.Ads))
		matchedNative := make([]bool, len(p.Native))
		doc := rules.NewDoc(res.Segments)
		for _, seg := range res.Segments {
			s := scored{page: p.File, id: seg.ID, text: seg.Text}
			features := rules.Detect(seg, doc, cfg.L2.Patterns)
			s.features = features
			s.score = rules.Score(features, cfg.L2)
			s.forced, s.shortcut = rules.Shortcut(features, cfg.L2)
			flat := strings.Join(strings.Fields(seg.Text), " ")
			for i, prefix := range p.Ads {
				if matchesLabel(flat, prefix) {
					s.ad, matched[i] = true, true
				}
			}
			for i, prefix := range p.Native {
				if matchesLabel(flat, prefix) {
					s.native, matchedNative[i] = true, true
				}
			}
			ev.segments = append(ev.segments, s)
		}

		for i, ok := range matched {
			if !ok {
				return ev, fmt.Errorf("calibrate: %s: ad label matches no segment: %q", p.File, p.Ads[i])
			}
		}
		for i, ok := range matchedNative {
			if !ok {
				return ev, fmt.Errorf("calibrate: %s: native label matches no segment: %q", p.File, p.Native[i])
			}
		}
		ev.pages = append(ev.pages, pageEval{label: p, hidden: len(res.Hidden) > 0})
	}
	return ev, nil
}

const unambiguousPrefix = 40

func matchesLabel(flat, label string) bool {
	if len([]rune(label)) < unambiguousPrefix {
		return flat == label
	}
	return strings.HasPrefix(flat, label)
}

func score(ev evaluation, hi, lo float64) metrics {
	l2 := config.L2{Hi: hi, Lo: lo}
	m := metrics{hi: hi, lo: lo}
	for _, s := range ev.segments {
		v := s.verdict(l2)
		m.segments++
		if v == core.Flag {
			m.flags++
		}
		switch {
		case v == core.Drop && s.commercial():
			m.tp++
		case v == core.Drop:
			m.fp++
		case s.ad:
			m.fn++
		}
		if s.ad {
			m.ads++
		}
		if s.native {
			m.nativeTotal++
			if v != core.Keep {
				m.nativeCaught++
			}
		}
		switch {
		case v == core.Keep:
		case s.ad:
			m.covered++
		default:
			m.noise++
		}
	}
	return m
}

func hiddenErrors(ev evaluation) []string {
	var out []string
	for _, p := range ev.pages {
		if p.hidden != p.label.Hidden {
			out = append(out, fmt.Sprintf("%s: hidden=%v, labelled %v", p.label.File, p.hidden, p.label.Hidden))
		}
	}
	return out
}

func grid(ev evaluation) []metrics {
	var out []metrics
	for hi := 0.15; hi <= 0.951; hi += 0.05 {
		for lo := 0.05; lo < hi-0.001; lo += 0.05 {
			out = append(out, score(ev, round(hi), round(lo)))
		}
	}
	return out
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

const (
	MinPrecision = 1.0
	MaxFlagRate  = 0.15
)

func better(a, b metrics) bool {
	if ok, other := a.precision() >= MinPrecision, b.precision() >= MinPrecision; ok != other {
		return ok
	}
	if ok, other := a.flagRate() <= MaxFlagRate, b.flagRate() <= MaxFlagRate; ok != other {
		return ok
	}
	if math.Abs(a.coverage()-b.coverage()) > 1e-9 {
		return a.coverage() > b.coverage()
	}
	if math.Abs(a.nativeCoverage()-b.nativeCoverage()) > 1e-9 {
		return a.nativeCoverage() > b.nativeCoverage()
	}
	return a.noise < b.noise
}

func safest(ms []metrics) metrics {
	sorted := append([]metrics(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return better(sorted[i], sorted[j]) })
	return sorted[0]
}

func report(w io.Writer, ev evaluation, l2 config.L2) error {
	ads := 0
	for _, s := range ev.segments {
		if s.ad {
			ads++
		}
	}
	native := 0
	for _, s := range ev.segments {
		if s.native {
			native++
		}
	}
	fmt.Fprintf(w, "corpus: %d pages, %d segments, %d marked ads, %d marked native\n\n", len(ev.pages), len(ev.segments), ads, native)

	g := grid(ev)
	fmt.Fprintln(w, "   hi     lo  drop_prec  drop_rec  caught  native  flag_rate  noise")
	for _, m := range topBy(g, 15) {
		printRow(w, m)
	}

	fmt.Fprintln(w, "\ncurrent config:")
	cur := score(ev, l2.Hi, l2.Lo)
	printRow(w, cur)
	fmt.Fprintln(w, "\nbest on the grid:")
	printRow(w, topBy(g, 1)[0])
	fmt.Fprintf(w, "\nbest with precision >= %.2f, then widest coverage:\n", MinPrecision)
	printRow(w, safest(g))

	if errs := hiddenErrors(ev); len(errs) > 0 {
		fmt.Fprintf(w, "\nl1 disagreements (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintln(w, " ", e)
		}
	} else {
		fmt.Fprintf(w, "\nl1: %d/%d pages agree with the labels\n", len(ev.pages), len(ev.pages))
	}

	fmt.Fprintln(w, "\ncut by mistake (a fact was removed):")
	for _, s := range ev.segments {
		if !s.commercial() && s.verdict(l2) == core.Drop {
			fmt.Fprintf(w, "  %s %s: %s\n", s.page, s.id, clip(s.text, 90))
		}
	}
	fmt.Fprintln(w, "\nmissed entirely (kept as fact):")
	for _, s := range ev.segments {
		if s.commercial() && s.verdict(l2) == core.Keep {
			fmt.Fprintf(w, "  %s %s [%.2f]: %s\n", s.page, s.id, s.score, clip(s.text, 90))
		}
	}
	fmt.Fprintln(w, "\nflagged, not cut (agent is warned):")
	for _, s := range ev.segments {
		if s.commercial() && s.verdict(l2) == core.Flag {
			fmt.Fprintf(w, "  %s %s [%.2f]: %s\n", s.page, s.id, s.score, clip(s.text, 90))
		}
	}
	return nil
}

func topBy(ms []metrics, n int) []metrics {
	sorted := append([]metrics(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return better(sorted[i], sorted[j]) })
	if len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}

func printRow(w io.Writer, m metrics) {
	fmt.Fprintf(w, "%5.2f  %5.2f  %9.3f  %8.3f  %6.3f  %6.3f  %9.3f  %5d\n",
		m.hi, m.lo, m.precision(), m.recall(), m.coverage(), m.nativeCoverage(), m.flagRate(), m.noise)
}

func dumpSegments(w io.Writer, ev evaluation, l2 config.L2) error {
	page := ""
	for _, s := range ev.segments {
		if s.page != page {
			page = s.page
			fmt.Fprintf(w, "\n== %s\n", page)
		}
		mark := " "
		if s.ad {
			mark = "*"
		}
		fmt.Fprintf(w, "%s %-5s %-4s %.2f %-32s %s\n", mark, s.id, s.verdict(l2), s.score, strings.Join(s.features, ","), clip(s.text, 140))
	}
	return nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
