package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/belaytzev/adfilter/internal/core"
)

const hexDigits = "0123456789abcdef"

func newMux(st *Store, g *guard) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/segments/{prefix}", func(w http.ResponseWriter, r *http.Request) {
		handleBucket(w, r, st)
	})
	mux.HandleFunc("POST /v1/segments", g.limit(func(w http.ResponseWriter, r *http.Request) {
		handleSubmit(w, r, st)
	}))
	mux.HandleFunc("POST /v1/vote", g.limit(func(w http.ResponseWriter, r *http.Request) {
		handleVote(w, r, st)
	}))
	return mux
}

func handleBucket(w http.ResponseWriter, r *http.Request, st *Store) {
	prefix := r.PathValue("prefix")
	if !validPrefix(prefix) {
		writeError(w, http.StatusBadRequest, "prefix must be "+strconv.Itoa(core.PrefixLen)+" lowercase hex characters")
		return
	}
	version, err := strconv.Atoi(r.URL.Query().Get("norm_version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "norm_version must be an integer")
		return
	}
	// A foreign normalization means foreign hashes: serving them would mix two
	// hash spaces and quietly rot both.
	if version != core.NormVersion {
		writeError(w, http.StatusBadRequest, "unsupported norm_version "+strconv.Itoa(version))
		return
	}

	entries, err := st.Bucket(prefix, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bucket unavailable")
		return
	}
	writeJSON(w, http.StatusOK, core.BucketResponse{
		Prefix:      prefix,
		NormVersion: version,
		Entries:     pad(prefix, entries),
	})
}

func validPrefix(p string) bool {
	if len(p) != core.PrefixLen {
		return false
	}
	return strings.Trim(p, hexDigits) == ""
}

// pad fills a short bucket with decoys sharing its prefix, so the size of a
// response says nothing about how many real verdicts the prefix holds. Decoys
// are derived from the prefix, not random: two lookups of the same bucket must
// return the same set, otherwise diffing the responses reveals the real rows.
// A client matches by full hash locally, so a decoy can never be mistaken for
// a verdict about its segment.
// ponytail: uniform decoy metadata, vary it if bucket fingerprinting matters
func pad(prefix string, entries []core.BucketEntry) []core.BucketEntry {
	if entries == nil {
		entries = []core.BucketEntry{}
	}
	real := make(map[string]bool, len(entries))
	for _, e := range entries {
		real[e.Hash] = true
	}
	for i := 0; len(entries) < core.MinBucket; i++ {
		sum := sha256.Sum256([]byte("adfilter/decoy/" + prefix + "/" + strconv.Itoa(i)))
		hash := prefix + hex.EncodeToString(sum[:])[core.PrefixLen:]
		if real[hash] {
			continue
		}
		entries = append(entries, core.BucketEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
	}
	return entries
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, core.ErrorResponse{Error: msg})
}
