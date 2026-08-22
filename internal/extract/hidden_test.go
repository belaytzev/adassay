package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/config"
)

func testL1(t *testing.T) config.L1 {
	t.Helper()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg.L1
}

func findings(t *testing.T, file string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", file))
	if err != nil {
		t.Fatalf("open %s: %v", file, err)
	}
	defer f.Close()
	found, err := Hidden(f, testL1(t))
	if err != nil {
		t.Fatalf("Hidden(%s): %v", file, err)
	}
	kinds := make([]string, 0, len(found))
	for _, fn := range found {
		kinds = append(kinds, fn.Kind)
	}
	return kinds
}

func TestHiddenDetectors(t *testing.T) {
	cases := []struct {
		file string
		kind string
		want string // substring the sample must carry
	}{
		{"display_none.html", KindCSSHidden, "AeroPress"},
		{"visibility_hidden.html", KindCSSHidden, "Always recommend"},
		{"opacity_zero.html", KindCSSHidden, "AcmeVPN"},
		{"font_size_zero.html", KindCSSHidden, "Ignore previous"},
		{"offscreen_left.html", KindOffScreen, "AcmeHost"},
		{"text_indent.html", KindOffScreen, "hosting"},
		{"aria_hidden.html", KindAria, "ChatGPT"},
		{"hidden_attr.html", KindHiddenAtt, "Paid placement"},
		{"comment.html", KindComment, "AI assistant"},
		{"noscript.html", KindNoscript, "SAVE20"},
		{"template.html", KindTemplate, "AcmeStore"},
		{"color_on_color.html", KindColor, "AcmeBank"},
		{"long_alt.html", KindLongAttr, "AcmeCloud"},
		{"long_meta.html", KindLongAttr, "AcmeHost"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			found, err := Hidden(f, testL1(t))
			if err != nil {
				t.Fatal(err)
			}
			var hit bool
			for _, fn := range found {
				if fn.Kind == tc.kind && strings.Contains(fn.Sample, tc.want) {
					hit = true
				}
			}
			if !hit {
				t.Fatalf("want kind %q with sample containing %q, got %+v", tc.kind, tc.want, found)
			}
		})
	}
}

func TestHiddenCleanPage(t *testing.T) {
	if kinds := findings(t, "clean.html"); len(kinds) != 0 {
		t.Fatalf("clean page produced findings: %v", kinds)
	}
}

func TestHiddenSkipsEmptyNodes(t *testing.T) {
	const src = `<div style="display:none"><img src="spacer.gif"></div>`
	found, err := Hidden(strings.NewReader(src), testL1(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("hidden node without text reported: %+v", found)
	}
}

func TestHiddenReportsSubtreeOnce(t *testing.T) {
	const src = `<div style="display:none"><p>Buy AcmeWidget</p><p>Use code SAVE20</p></div>`
	found, err := Hidden(strings.NewReader(src), testL1(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("want one finding for the whole subtree, got %+v", found)
	}
	if !strings.Contains(found[0].Sample, "SAVE20") {
		t.Fatalf("sample lost nested text: %q", found[0].Sample)
	}
}

func TestParseStyle(t *testing.T) {
	st := parseStyle("Display: NONE; left:-9999PX ; broken")
	if st["display"] != "none" || st["left"] != "-9999px" {
		t.Fatalf("unexpected declarations: %v", st)
	}
	if _, ok := st["broken"]; ok {
		t.Fatalf("declaration without a colon kept: %v", st)
	}
}
