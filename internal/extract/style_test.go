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

		{"color:oklch(0 0 0/0)", KindColor},
		{"color:lab(0 0 0 / 0%)", KindColor},
		{"color:hwb(0 0% 0% / 0)", KindColor},
		{"background:url(a/*);display:none;list-style:url(*/b)", KindCSSHidden},
		{`background:url("a/*");display:none`, KindCSSHidden},
		{"background:URL(a/*);display:none", KindCSSHidden},

		{`background:url(a\)/*);display:none`, KindCSSHidden},
		{"background:url( a/* );display:none", KindCSSHidden},
		{"color:'a\n;display:none", KindCSSHidden},
		{"display:block;content:\"x\n;display:none", KindCSSHidden},
		{`background:u\72l(a/*);display:none`, KindCSSHidden},
		{`background:\75 rl(a/*);display:none`, KindCSSHidden},
		{"color:hsl(0deg 0% 0% / 0)", KindColor},
		{"color:rgb(0 0 0 / none)", KindColor},
		{`width:1\';display:none`, KindCSSHidden},
		{`width:1\(;display:none`, KindCSSHidden},
		{`\64 isplay:none`, KindCSSHidden},
		{`display:\6e one`, KindCSSHidden},
		{`DISPLAY:\4E ONE`, KindCSSHidden},
		{"display:\\6e\fone", KindCSSHidden},
		{"display:\\6e\rone", KindCSSHidden},
		{"\\64\fisplay:none", KindCSSHidden},
		{"\\64\r\nisplay:none", KindCSSHidden},
		{"display:\\6e\r\none", KindCSSHidden},
		{`display:none;display\ :block`, KindCSSHidden},

		{"color:rgb(0, 0, 0)", ""},
		{"color:rgba(0, 0, 0, 0.5)", ""},
		{"color:rgb(255 0 0 0)", ""},
		{"color:oklch(0 0 0 / 1)", ""},
		{"color:oklch(0,0,0,0)", ""},
		{"color:rgb(255 0 0 / 0 junk)", ""},
		{"color:rgb(a b c / 0)", ""},
		{"background:url(a/*);color:red", ""},
		{"background:xurl(/*);display:none", ""},
		{"color:foo-url(a/*);display:none;*/;color:red", ""},
		{"background:url(a/*;display:none;foo)", ""},
		{`background:url("a");display:none/*`, KindCSSHidden},
		{"background:calc(1px;display:none)", ""},
		{"color:black;--x:[;display:none;]", ""},
		{"color:black;--x:{;display:none;}", ""},
		{"color:black;color:rgb(1px 0 0 / 0)", ""},
		{"color:rgb(from red r g b / 0)", ""},
		{"color:black;--x:[);display:none;]", ""},
		{`width:1\;display:none`, ""},
		{"display:\\\nnone", ""},
		{"\\\ndisplay:none", ""},
		{"display:\\6e  one", ""},
		{"display:\\6e \tone", ""},
		{`display:\20none`, ""},
		{"color:rgba(0, 0, 0, none)", ""},
		{"color:rgba(none, 0, 0, 0)", ""},
		{"color:color(display-p3 1 0 0)", ""},
		{"color:device-cmyk(1 0 0 0)", ""},
		{`--a:"/*";display:none;--b:"*/"`, KindCSSHidden},
		{`content:"/*";color:red`, ""},
		{`content:'a\'/*';display:none`, KindCSSHidden},
		{"display:none;/* unterminated", KindCSSHidden},
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
