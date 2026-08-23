package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"adassay.com/internal/core"
)

const (
	maxBody  = 1 << 20
	maxBatch = 256
)

func handleSubmit(w http.ResponseWriter, r *http.Request, st *Store, g *guard) {
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
	// The limiter outside already charged one token for the request. What it
	// cannot see is that a request carries up to maxBatch verdicts: without
	// charging for the rest, a batch multiplies the write rate by maxBatch.
	if !g.lim.allowN(clientIP(r, g.trusted), len(req.Entries)-1) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many writes from this address")
		return
	}

	var resp core.SubmitResponse
	for _, e := range req.Entries {
		if !validEntry(e) {
			resp.Rejected++
			continue
		}
		accepted, _, err := st.submit(e, req.NormVersion, req.ClientID)
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
	}, req.NormVersion, req.ClientID)
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
	// lookup becomes a second vote for a verdict nobody re-derived. SourceHuman
	// outranks everything, so accepting it here would let one batch overturn
	// 256 verdicts at once — a human correction goes through /v1/vote, one
	// hash per request.
	if !core.ValidSource(e.Source) || e.Source == core.SourceShared || e.Source == core.SourceHuman {
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

// validClientID checks the shape of a UUID, dashes in their places included:
// without the positions "------------------------------------" would pass and
// a quorum could be reached with identifiers nobody had to generate.
func validClientID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune(hexDigits, c) {
				return false
			}
		}
	}
	return true
}

func validReason(reason string) bool { return core.ValidReason(reason) }
