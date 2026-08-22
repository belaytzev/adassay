package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/belaytzev/adfilter/internal/core"
)

const (
	maxBody   = 1 << 20
	maxBatch  = 256
	maxReason = 48
)

func handleSubmit(w http.ResponseWriter, r *http.Request, st *Store) {
	var req core.SubmitRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !checkEnvelope(w, req.NormVersion, req.ClientID) {
		return
	}
	if len(req.Entries) == 0 || len(req.Entries) > maxBatch {
		writeError(w, http.StatusBadRequest, "entries must hold between 1 and "+strconv.Itoa(maxBatch)+" verdicts")
		return
	}

	var resp core.SubmitResponse
	for _, e := range req.Entries {
		if !validEntry(e) {
			resp.Rejected++
			continue
		}
		accepted, _, err := st.submit(e, req.NormVersion)
		switch {
		case err != nil:
			slog.Error("submit failed", "prefix", e.Hash[:core.PrefixLen], "err", err)
			writeError(w, http.StatusInternalServerError, "verdict not stored")
			return
		case accepted:
			resp.Accepted++
		default:
			resp.Rejected++
		}
	}
	slog.Info("submit", "accepted", resp.Accepted, "rejected", resp.Rejected)
	writeJSON(w, http.StatusOK, resp)
}

// handleVote is the human override: a vote outranks anything the rules or the
// judge decided, which is the only correction the shared database has against
// a heuristic that is confidently wrong everywhere at once.
func handleVote(w http.ResponseWriter, r *http.Request, st *Store) {
	var req core.VoteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !checkEnvelope(w, req.NormVersion, req.ClientID) {
		return
	}
	if !validHash(req.Hash) {
		writeError(w, http.StatusBadRequest, "hash must be 64 lowercase hex characters")
		return
	}
	if req.Verdict != core.Keep && req.Verdict != core.Drop {
		writeError(w, http.StatusBadRequest, "a vote is keep or drop")
		return
	}

	_, votes, err := st.submit(core.SubmitEntry{
		Hash:    req.Hash,
		Verdict: req.Verdict,
		Source:  core.SourceHuman,
	}, req.NormVersion)
	if err != nil {
		slog.Error("vote failed", "prefix", req.Hash[:core.PrefixLen], "err", err)
		writeError(w, http.StatusInternalServerError, "vote not stored")
		return
	}
	slog.Info("vote", "votes", votes)
	writeJSON(w, http.StatusOK, core.VoteResponse{Hash: req.Hash, Votes: votes})
}

// decodeBody refuses anything that is not exactly the wire type. The decoder
// error is never echoed or logged: a rejected body may carry the very text the
// database exists to keep out.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON object with known fields only")
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "body must hold a single JSON object")
		return false
	}
	return true
}

func checkEnvelope(w http.ResponseWriter, normVersion int, clientID string) bool {
	if normVersion != core.NormVersion {
		writeError(w, http.StatusBadRequest, "unsupported norm_version "+strconv.Itoa(normVersion))
		return false
	}
	if !validClientID(clientID) {
		writeError(w, http.StatusBadRequest, "client_id must be a UUID")
		return false
	}
	return true
}

func validEntry(e core.SubmitEntry) bool {
	// SourceShared would be the database quoting itself back: one client's
	// lookup becomes a second vote for a verdict nobody re-derived.
	if !core.ValidSource(e.Source) || e.Source == core.SourceShared {
		return false
	}
	if !validHash(e.Hash) {
		return false
	}
	for _, reason := range e.Reasons {
		if !validReason(reason) {
			return false
		}
	}
	return true
}

func validHash(h string) bool {
	return len(h) == 64 && strings.Trim(h, hexDigits) == ""
}

func validClientID(id string) bool {
	return len(id) == 36 && strings.Trim(id, hexDigits+"-") == ""
}

// validReason keeps reasons to rule identifiers. Free-form reasons are the one
// field wide enough to smuggle article text into a database that stores none.
func validReason(reason string) bool {
	if reason == "" || len(reason) > maxReason {
		return false
	}
	return strings.Trim(reason, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}
