package extract

import "testing"

func TestStyleKindInvisibleSpellings(t *testing.T) {
	cases := []struct {
		style string
		want  string
	}{
		{"display:/**/none", KindCSSHidden},
		{"display:none/* keep */;color:red", KindCSSHidden},
		{"color:transparent", KindColor},
		{"color: rgba(0, 0, 0, 0)", KindColor},
		{"color:hsl(0 0% 0% / 0)", KindColor},
		{"-webkit-text-fill-color:transparent", KindColor},
		{"position:absolute;clip:rect(0,0,0,0)", KindCSSHidden},
		{"clip: rect(1px, 1px, 1px, 1px)", KindCSSHidden},
		{"clip-path:inset(50%)", KindCSSHidden},
		{"clip-path:inset(0 100%)", KindCSSHidden},
		{"width:0;height:0;overflow:hidden", KindCSSHidden},
		{"max-height:0;overflow:hidden", KindCSSHidden},

		{"color:rgb(0, 0, 0)", ""},
		{"color:rgba(0, 0, 0, 0.5)", ""},
		{"clip:rect(0, 100px, 100px, 0)", ""},
		{"clip:auto", ""},
		{"clip-path:inset(10% 20%)", ""},
		{"clip-path:inset(50px)", ""},
		{"width:0;overflow:visible", ""},
		{"display:block/* none */", ""},
	}
	for _, tc := range cases {
		if got := styleKind(parseStyle(tc.style)); got != tc.want {
			t.Errorf("styleKind(%q) = %q, want %q", tc.style, got, tc.want)
		}
	}
}
