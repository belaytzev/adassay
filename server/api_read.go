package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"adassay.com/internal/core"
)

const hexDigits = "0123456789abcdef"

func newMux(st *Store, g *guard, m *metrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/segments/{prefix}", m.count(&m.bucketReqs, func(w http.ResponseWriter, r *http.Request) {
		handleBucket(w, r, st, m)
	}))

	mux.HandleFunc("POST /v1/segments", m.count(&m.submitReqs, g.limit(func(w http.ResponseWriter, r *http.Request) {
		handleSubmit(w, r, st, g)
	})))
	mux.HandleFunc("POST /v1/vote", m.count(&m.voteReqs, g.limit(func(w http.ResponseWriter, r *http.Request) {
		handleVote(w, r, st)
	})))
	mux.HandleFunc("POST /v1/register", g.limit(func(w http.ResponseWriter, r *http.Request) {
		handleRegister(w, r, st)
	}))
	mux.HandleFunc("GET /metrics", m.handler(st))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func handleBucket(w http.ResponseWriter, r *http.Request, st *Store, m *metrics) {
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

	if version != core.NormVersion {
		writeError(w, http.StatusBadRequest, "unsupported norm_version "+strconv.Itoa(version))
		return
	}

	entries, err := st.Bucket(prefix, version)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "bucket unavailable")
		return
	}

	if len(entries) > 0 {
		m.bucketHits.Add(1)
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

func pad(prefix string, entries []core.BucketEntry) []core.BucketEntry {
	if entries == nil {
		entries = []core.BucketEntry{}
	}
	real := make(map[string]bool, len(entries))
	for _, e := range entries {
		real[e.Hash] = true
	}
	for i := 0; len(entries) < core.MinBucket; i++ {
		sum := sha256.Sum256([]byte("adassay/decoy/" + prefix + "/" + strconv.Itoa(i)))
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
