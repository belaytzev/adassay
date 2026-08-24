package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"adassay.com/internal/core"
)

const (
	headerInstall = "X-Adassay-Install"
	headerSecret  = "X-Adassay-Secret"
)

func handleRegister(w http.ResponseWriter, _ *http.Request, st *Store) {
	id, secret, err := registerInstall(st.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not register")
		return
	}
	writeJSON(w, http.StatusOK, core.RegisterResponse{ClientID: id, Secret: secret})
}

func authenticate(w http.ResponseWriter, r *http.Request, st *Store, v any) (install, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body is too large or unreadable")
		return install{}, false
	}
	id := r.Header.Get(headerInstall)
	if !validClientID(id) {
		writeError(w, http.StatusUnauthorized, "register the install first: POST /v1/register")
		return install{}, false
	}
	in, err := loadInstall(st.db, id)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "register the install first: POST /v1/register")
		return install{}, false
	}
	if !in.verify(r.Header.Get(headerSecret)) {
		writeError(w, http.StatusUnauthorized, "install secret does not match")
		return install{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON object with known fields only")
		return install{}, false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "body must hold a single JSON object")
		return install{}, false
	}
	return in, true
}
