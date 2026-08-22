package store

import (
	"encoding/hex"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
)

// golden pins the hash space of NormVersion 1. These digests are the addresses
// the shared database is built on: if a normalization change moves them, every
// stored verdict stops matching and nothing else in the suite would notice.
func TestGoldenVectors(t *testing.T) {
	if core.NormVersion != 1 {
		t.Fatalf("NormVersion is %d: add vectors for it, do not edit the ones below", core.NormVersion)
	}
	golden := map[string]string{
		"Use promo code ADFILTER for 20% off": "6264d3ad096344259913afc5c9f6ee7606786a0d8f58ded7c08615ca4cc60cda",
		"Партнёрский материал":                "191ca0249f59f0a633e91ac01560051d03bc68789bbfb4b0b081fbb36e3ed0d1",
		"":         "2ecfadd1b80d67dbb5fa4e99dd9148db67ae80b1afbe79266f7f6b964e6f3669",
		"habr.com": "bb9e1d5d5779ab373fc705224656829be7db07125f07ca0c99f2cdd56508ee68",
	}
	for text, want := range golden {
		if got := HexHash(text); got != want {
			t.Errorf("HexHash(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestNormalizationEquivalence(t *testing.T) {
	base := "Use promo code ADFILTER for 20% off"
	same := []struct {
		name string
		text string
	}{
		{"case", "USE PROMO CODE adfilter FOR 20% OFF"},
		{"whitespace", "  Use   promo\tcode\nADFILTER for  20% off  "},
		{"nbsp", "Use\u00a0promo code ADFILTER for 20% off"},
		{"zero width", "Use pro\u200bmo code ADFILTER\ufeff for 20% off"},
		{"tag characters", "Use promo code\U000E0041 ADFILTER for 20% off"},
		{"nfkc fullwidth", "Use promo code ＡＤＦＩＬＴＥＲ for ２０% off"},
	}
	want := HexHash(base)
	for _, tc := range same {
		if got := HexHash(tc.text); got != want {
			t.Errorf("%s: HexHash(%q) = %s, want %s", tc.name, tc.text, got, want)
		}
	}
}

func TestDifferentTextDiffersInHash(t *testing.T) {
	texts := []string{
		"Use promo code ADFILTER for 20% off",
		"Use promo code ADFILTER for 30% off",
		"Use promo code ADFILTERX for 20% off",
		"the compiler emits a warning here",
	}
	seen := map[string]string{}
	for _, text := range texts {
		h := HexHash(text)
		if prev, dup := seen[h]; dup {
			t.Errorf("%q and %q collide on %s", prev, text, h)
		}
		seen[h] = text
	}
}

func TestNormVersionSplitsBuckets(t *testing.T) {
	const text = "Use promo code ADFILTER for 20% off"
	if a, b := hashVersion(text, core.NormVersion), hashVersion(text, core.NormVersion+1); string(a) == string(b) {
		t.Fatal("a version bump must move the hash, otherwise old and new normalization share a bucket")
	}
}

func TestPrefixIsHexOfConfiguredLength(t *testing.T) {
	p := Prefix(Hash("Партнёрский материал"))
	if len(p) != core.PrefixLen {
		t.Fatalf("Prefix length = %d, want %d", len(p), core.PrefixLen)
	}
	if _, err := hex.DecodeString(p); err != nil {
		t.Fatalf("Prefix %q is not hex: %v", p, err)
	}
}

func TestDomainIsHashedNotStoredInClear(t *testing.T) {
	h := HashDomain("habr.com")
	if string(h) == "habr.com" || HexHash("habr.com") != hex.EncodeToString(h) {
		t.Fatal("HashDomain must run the domain through the segment hash")
	}
}
