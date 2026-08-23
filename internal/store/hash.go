package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"adassay.com/internal/core"
	"golang.org/x/text/unicode/norm"
)

func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	s = strings.Map(dropInvisible, s)
	return strings.Join(strings.Fields(s), " ")
}

func dropInvisible(r rune) rune {
	if r >= 0xE0000 && r <= 0xE007F {
		return -1
	}
	switch r {
	case 0x200B, 0x200C, 0x200D, 0x2060, 0xFEFF:
		return -1
	}
	return r
}

func Hash(s string) []byte {
	return hashVersion(s, core.NormVersion)
}

func HashDomain(domain string) []byte {
	return Hash(domain)
}

func hashVersion(s string, version int) []byte {
	sum := sha256.Sum256([]byte("adassay/v" + strconv.Itoa(version) + "\n" + Normalize(s)))
	return sum[:]
}

func HexHash(s string) string {
	return hex.EncodeToString(Hash(s))
}

func Prefix(hash []byte) string {
	return hex.EncodeToString(hash)[:core.PrefixLen]
}
