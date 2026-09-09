package share

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"adassay.com/internal/core"
)

func TestRefusedBatchStaysInTheSpool(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req core.SubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode submit: %v", err)
		}
		json.NewEncoder(w).Encode(core.SubmitResponse{Rejected: len(req.Entries)})
	}))
	t.Cleanup(srv.Close)
	client := &Client{BaseURL: srv.URL, HTTP: srv.Client(), ID: "test-client"}

	spool := spoolOf(5, 24*time.Hour)
	err := NewOutbox(spool, client, false).FlushNow()
	if err == nil {
		t.Fatal("a batch the server stored nothing from must not count as delivered")
	}
	if calls != 1 {
		t.Fatalf("server called %d times, want once", calls)
	}
	if len(spool.entries) != 5 || spool.cleared != nil {
		t.Fatalf("spool = %d entries, cleared %v; want all 5 kept for a later flush", len(spool.entries), spool.cleared)
	}
}
