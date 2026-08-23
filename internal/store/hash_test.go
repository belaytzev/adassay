package store

import (
	"encoding/hex"
	"testing"

	"adassay.com/internal/core"
)

// golden pins the hash space of NormVersion 1. These digests are the addresses
// the shared database is built on: if a normalization change moves them, every
// stored verdict stops matching and nothing else in the suite would notice.
func TestGoldenVectors(t *testing.T) {
	if core.NormVersion != 1 {
		t.Fatalf("NormVersion is %d: add vectors for it, do not edit the ones below", core.NormVersion)
	}
	golden := map[string]string{
		"Use promo code ACME for 20% off": "af3ecb19aed1af8d435c679bf1989e28be73cddea544d0eb2dff76f121f9b73d",
		"Партнёрский материал":            "b6ff88765d509aad7bee8570b6058d15b3bf9fd3a8220e4ee624e560c382ca04",
		"":         "9ff1979be855c23b41fdabea333f14bd644740f5cff3425f32adfc874f486d82",
		"habr.com": "490d5498b4bf09de6e1b0c98af234095cff636719c87a2171a527dfa6e3262eb",
	}
	for text, want := range golden {
		if got := HexHash(text); got != want {
			t.Errorf("HexHash(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestNormalizationEquivalence(t *testing.T) {
	base := "Use promo code ACME for 20% off"
	same := []struct {
		name string
		text string
	}{
		{"case", "USE PROMO CODE acme FOR 20% OFF"},
		{"whitespace", "  Use   promo\tcode\nACME for  20% off  "},
		{"nbsp", "Use\u00a0promo code ACME for 20% off"},
		{"zero width", "Use pro\u200bmo code ACME\ufeff for 20% off"},
		{"tag characters", "Use promo code\U000E0041 ACME for 20% off"},
		{"nfkc fullwidth", "Use promo code ＡＣＭＥ for ２０% off"},
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
		"Use promo code ACME for 20% off",
		"Use promo code ACME for 30% off",
		"Use promo code ACMEX for 20% off",
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
	const text = "Use promo code ACME for 20% off"
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
