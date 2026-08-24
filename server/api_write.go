package server

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
	in, ok := authenticate(w, r, st, &req)
	if !ok {
		return
	}
	if !checkEnvelope(w, req.NormVersion, req.ClientID) {
		return
	}
	if req.ClientID != in.clientID {
		writeError(w, http.StatusUnauthorized, "client_id does not match the signing install")
		return
	}
	if len(req.Entries) == 0 || len(req.Entries) > maxBatch {
		writeError(w, http.StatusBadRequest, "entries must hold between 1 and "+strconv.Itoa(maxBatch)+" verdicts")
		return
	}

	if !g.lim.allowN(clientIP(r, g.trusted), len(req.Entries)-1) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "too many writes from this address")
		return
	}

	var resp core.SubmitResponse
	for _, e := range req.Entries {
		if e.Source == core.SourceSeed && !in.seeder {
			resp.Rejected++
			continue
		}
		if !core.ValidSubmitEntry(e) {
			resp.Rejected++
			continue
		}
		accepted, _, err := st.submit(e, req.NormVersion, req.ClientID, in.seeder)
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

func handleVote(w http.ResponseWriter, r *http.Request, st *Store) {
	var req core.VoteRequest
	in, ok := authenticate(w, r, st, &req)
	if !ok {
		return
	}
	if !checkEnvelope(w, req.NormVersion, req.ClientID) {
		return
	}
	if req.ClientID != in.clientID {
		writeError(w, http.StatusUnauthorized, "client_id does not match the signing install")
		return
	}
	if !core.ValidHash(req.Hash) {
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
	}, req.NormVersion, req.ClientID, false)
	if err != nil {
		slog.Error("vote failed", "prefix", req.Hash[:core.PrefixLen], "err", err)
		writeError(w, http.StatusInternalServerError, "vote not stored")
		return
	}
	slog.Info("vote", "votes", votes)
	writeJSON(w, http.StatusOK, core.VoteResponse{Hash: req.Hash, Votes: votes})
}

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
