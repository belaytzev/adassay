// Package store keeps verdicts and per-domain counters in a local sqlite file.
// Nothing readable leaves this package: segments and domains are stored and
// queried by hash, so neither the database file nor a request to the shared
// backend reveals what was read.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/belaytzev/adfilter/internal/core"
	"golang.org/x/text/unicode/norm"
)

// Normalize strips everything that differs between two copies of the same
// boilerplate: compatibility forms, case, invisible padding and whitespace.
// Any change here is a change of the hash space and must bump core.NormVersion.
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

// Hash is the bucket key of a segment. The version prefix is inside the digest,
// so hashes made by different normalization algorithms cannot collide even by
// accident — an old and a new bucket are simply different addresses.
func Hash(s string) []byte {
	return hashVersion(s, core.NormVersion)
}

// HashDomain runs a domain through the same function as a segment. Sending
// habr.com in the clear would hand the backend a reading history.
func HashDomain(domain string) []byte {
	return Hash(domain)
}

func hashVersion(s string, version int) []byte {
	sum := sha256.Sum256([]byte("adfilter/v" + strconv.Itoa(version) + "\n" + Normalize(s)))
	return sum[:]
}

func HexHash(s string) string {
	return hex.EncodeToString(Hash(s))
}

// Prefix is what a client may send to the shared database: the leading hex
// characters of a hash, enough to fetch a bucket and not enough to identify the
// segment inside it.
func Prefix(hash []byte) string {
	return hex.EncodeToString(hash)[:core.PrefixLen]
}
