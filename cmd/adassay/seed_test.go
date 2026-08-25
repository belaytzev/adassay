package main

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestAtomEntryTakesTheArticleNotTheComments(t *testing.T) {
	const doc = `<feed xmlns="http://www.w3.org/2005/Atom">
	  <entry>
	    <link rel="alternate" type="text/html" href="https://example.com/article"/>
	    <link rel="replies" type="application/atom+xml" href="https://example.com/article/feed/"/>
	    <link rel="replies" type="text/html" href="https://example.com/article#comments"/>
	    <published>Mon, 02 Jan 2026 15:04:05 +0000</published>
	  </entry>
	</feed>`

	var f feed
	if err := xml.Unmarshal([]byte(doc), &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(f.Entries))
	}
	if got := f.Entries[0].article(); got != "https://example.com/article" {
		t.Errorf("article = %q, want the alternate link: encoding/xml keeps the last matching "+
			"element, which on a WordPress feed is the comment thread", got)
	}
}

func TestFreshRefusesWhatItCannotDate(t *testing.T) {
	cutoff := time.Now().Add(-24 * time.Hour)
	cases := []struct {
		name  string
		stamp string
		want  bool
	}{
		{"no stamp at all", "", false},
		{"unparseable", "sometime last week", false},
		{"bare date, inside", time.Now().Format(time.DateOnly), true},
		{"single-digit day", time.Now().Format("Mon, 2 Jan 2006 15:04:05 -0700"), true},
		{"rfc1123z, outside", time.Now().Add(-72 * time.Hour).Format(time.RFC1123Z), false},
		{"rfc1123z, inside", time.Now().Add(-time.Hour).Format(time.RFC1123Z), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fresh(cutoff, tc.stamp); got != tc.want {
				t.Errorf("fresh(%q) = %v, want %v: --since is documented as a window, so an entry "+
					"nobody can date must not count as inside it", tc.stamp, got, tc.want)
			}
		})
	}
}

func TestSeedRefusesANonPositiveLimit(t *testing.T) {
	var out strings.Builder
	err := seed([]string{"--urls", "/dev/null", "--limit", "-1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Errorf("err = %v, want a flag error: a negative limit reaches targets[:-1] and panics", err)
	}
}
