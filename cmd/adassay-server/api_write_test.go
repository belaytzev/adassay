package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"adassay.com/internal/core"
	"adassay.com/internal/extract"
	"adassay.com/internal/judge"
	"adassay.com/internal/share"
	"adassay.com/internal/store"
)

const testClient = "6f1c9f4e-2b8a-4c1d-9f3e-0a7b5c2d8e10"

func post(t *testing.T, st *Store, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	testMux(st).ServeHTTP(w, r)
	return w
}

func postJSON(t *testing.T, st *Store, path string, v any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return post(t, st, path, string(b))
}

func submit(t *testing.T, st *Store, entries ...core.SubmitEntry) core.SubmitResponse {
	t.Helper()
	w := postJSON(t, st, "/v1/segments", core.SubmitRequest{
		ClientID:    testClient,
		NormVersion: core.NormVersion,
		Entries:     entries,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	var resp core.SubmitResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

// stored returns the row the bucket endpoint reports for hash, which is the
// only view a client ever gets.
func stored(t *testing.T, st *Store, hash string) core.BucketEntry {
	t.Helper()
	bucket := decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion)))
	for _, e := range bucket.Entries {
		if e.Hash == hash {
			return e
		}
	}
	t.Fatalf("hash %q is not in its bucket", hash)
	return core.BucketEntry{}
}

func TestSubmitStoresBatch(t *testing.T) {
	st := newTestStore(t)
	first, second := store.HexHash("Материал подготовлен при поддержке"), store.HexHash("Промокод ACME даёт 20%")

	resp := submit(t, st,
		core.SubmitEntry{Hash: first, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules},
		core.SubmitEntry{Hash: second, Verdict: core.Flag, Reasons: []string{"promo_code"}, Source: core.SourceOllama},
	)
	if resp.Accepted != 2 || resp.Rejected != 0 {
		t.Fatalf("response = %+v, want both entries accepted", resp)
	}

	got := stored(t, st, first)
	if got.Verdict != core.Drop || got.Source != core.SourceRules || got.Votes != 1 ||
		len(got.Reasons) != 1 || got.Reasons[0] != "disclaimer" {
		t.Fatalf("entry = %+v, want the submitted verdict", got)
	}
	if got := stored(t, st, second); got.Verdict != core.Flag || got.Source != core.SourceOllama {
		t.Fatalf("entry = %+v, want the second submitted verdict", got)
	}
}

func TestSubmitCountsAgreementAsVote(t *testing.T) {
	st := newTestStore(t)
	mux := testMux(st)
	hash := store.HexHash("Партнёрский материал")
	entry := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules}

	submitAs(t, mux, client(1), "", "", entry)
	if code := submitAs(t, mux, client(2), "", "", entry); code != http.StatusOK {
		t.Fatalf("status = %d, want the confirmation accepted", code)
	}
	if got := stored(t, st, hash); got.Votes != 2 {
		t.Fatalf("votes = %d, want 2 after a second client submitted the same verdict", got.Votes)
	}
}

func vote(t *testing.T, st *Store, hash string, verdict core.Verdict) core.VoteResponse {
	t.Helper()
	w := postJSON(t, st, "/v1/vote", core.VoteRequest{
		ClientID:    testClient,
		NormVersion: core.NormVersion,
		Hash:        hash,
		Verdict:     verdict,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	var resp core.VoteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return resp
}

// A human verdict outranks everything, so the batch endpoint must not carry
// one: /v1/segments would let a single request overturn maxBatch verdicts,
// while /v1/vote costs one request per hash.
func TestSubmitRejectsHumanSource(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("не человек это писал")
	submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})

	resp := submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceHuman})
	if resp.Accepted != 0 || resp.Rejected != 1 {
		t.Fatalf("response = %+v, want a human-sourced batch entry rejected", resp)
	}
	if got := stored(t, st, hash); got.Verdict != core.Drop || got.Source != core.SourceRules {
		t.Fatalf("entry = %+v, want the rules verdict untouched", got)
	}
}

// A vote agrees with what is stored and carries no reasons of its own. Taking
// its empty list would strip the verdict of the only explanation it has.
func TestVoteKeepsExistingReasons(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("причины переживают голос")
	submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer", "affiliate_link"}, Source: core.SourceRules})

	vote(t, st, hash, core.Drop)
	if got := stored(t, st, hash); len(got.Reasons) != 2 {
		t.Fatalf("entry = %+v, want the reasons the rules supplied", got)
	}
}

// SourceRules < SourceOllama < SourceHuman: a weaker origin must not be able to
// overturn a verdict a stronger one produced.
func TestSubmitWeighsBySource(t *testing.T) {
	st := newTestStore(t)

	t.Run("stronger source overturns", func(t *testing.T) {
		hash := store.HexHash("судья перебивает правила")
		submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
		if resp := submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceOllama}); resp.Accepted != 1 {
			t.Fatalf("response = %+v, want the stronger source accepted", resp)
		}
		got := stored(t, st, hash)
		if got.Verdict != core.Drop || got.Source != core.SourceOllama || got.Votes != 1 {
			t.Fatalf("entry = %+v, want the ollama verdict with a reset vote count", got)
		}
	})

	t.Run("weaker source cannot overturn", func(t *testing.T) {
		hash := store.HexHash("правила не перебивают человека")
		vote(t, st, hash, core.Drop)
		for _, weaker := range []string{core.SourceOllama, core.SourceRules} {
			resp := submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: weaker})
			if resp.Accepted != 0 || resp.Rejected != 1 {
				t.Fatalf("response = %+v, want %s rejected against a human verdict", resp, weaker)
			}
		}
		if got := stored(t, st, hash); got.Verdict != core.Drop || got.Source != core.SourceHuman {
			t.Fatalf("entry = %+v, want the human verdict untouched", got)
		}
	})

	t.Run("agreement keeps the strongest source", func(t *testing.T) {
		hash := store.HexHash("согласие не понижает происхождение")
		vote(t, st, hash, core.Drop)
		submitAs(t, testMux(st), client(7), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})
		got := stored(t, st, hash)
		if got.Source != core.SourceHuman || got.Votes != 2 {
			t.Fatalf("entry = %+v, want a human-sourced row with 2 votes", got)
		}
	})

	// Inside a tier the row belongs to whoever wrote it first, but only until
	// an equal-rank verdict gathers a quorum of its own. Turning the
	// disagreement away instead would let one stranger's claim on a hash block
	// the honest verdict for good.
	t.Run("equal rank takes its own quorum", func(t *testing.T) {
		st := newTestStore(t)
		st.quorum = 2
		mux := testMux(st)
		hash := store.HexHash("ничья внутри уровня")

		drop := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}
		keep := core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules}
		submitAs(t, mux, client(1), "", "", drop)
		submitAs(t, mux, client(2), "", "", drop)

		submitAs(t, mux, client(3), "", "", keep)
		if got := stored(t, st, hash); got.Verdict != core.Drop {
			t.Fatalf("entry = %+v, want the first verdict kept against a lone challenger", got)
		}
		submitAs(t, mux, client(4), "", "", keep)
		if got := stored(t, st, hash); got.Verdict != core.Keep {
			t.Fatalf("entry = %+v, want the challenger's verdict once it has a quorum", got)
		}
	})
}

func TestSubmitRejectsBadRequests(t *testing.T) {
	hash := store.HexHash("что угодно")
	entry := `{"hash":"` + hash + `","verdict":"drop","source":"rules"}`
	envelope := func(body string) string {
		return `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion) + `,` + body + `}`
	}

	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"unknown field in body", envelope(`"entries":[` + entry + `],"debug":true`)},
		{"unknown field in entry", envelope(`"entries":[{"hash":"` + hash + `","verdict":"drop","source":"rules","text":"реклама"}]`)},
		{"foreign norm version", `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion+1) + `,"entries":[` + entry + `]}`},
		{"missing client id", `{"norm_version":` + strconv.Itoa(core.NormVersion) + `,"entries":[` + entry + `]}`},
		{"malformed client id", `{"client_id":"nope","norm_version":` + strconv.Itoa(core.NormVersion) + `,"entries":[` + entry + `]}`},
		// Right length, no work behind it: a quorum of these costs nothing.
		{"client id of dashes", `{"client_id":"------------------------------------","norm_version":` + strconv.Itoa(core.NormVersion) + `,"entries":[` + entry + `]}`},
		{"empty batch", envelope(`"entries":[]`)},
		{"unknown verdict", envelope(`"entries":[{"hash":"` + hash + `","verdict":"burn","source":"rules"}]`)},
		{"trailing object", envelope(`"entries":[`+entry+`]`) + `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			w := post(t, st, "/v1/segments", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}
			var e core.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error == "" {
				t.Fatalf("body = %q, want an error message", w.Body.String())
			}
		})
	}
}

// Entry-level junk is counted, not fatal: one bad row must not throw away a
// batch that is otherwise fine.
func TestSubmitRejectsBadEntries(t *testing.T) {
	st := newTestStore(t)
	good := store.HexHash("нормальный сегмент")
	hash := store.HexHash("плохой сегмент")

	resp := submit(t, st,
		core.SubmitEntry{Hash: good, Verdict: core.Drop, Source: core.SourceRules},
		core.SubmitEntry{Hash: "beef", Verdict: core.Drop, Source: core.SourceRules},
		core.SubmitEntry{Hash: strings.ToUpper(hash), Verdict: core.Drop, Source: core.SourceRules},
		core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: "invented"},
		core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceShared},
		core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules,
			Reasons: []string{"Купите наш курс со скидкой 50% прямо сейчас"}},
	)
	if resp.Accepted != 1 || resp.Rejected != 5 {
		t.Fatalf("response = %+v, want 1 accepted and 5 rejected", resp)
	}
	bucket := decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion)))
	for _, e := range bucket.Entries {
		if e.Hash == hash {
			t.Fatalf("rejected entries were stored anyway: %+v", e)
		}
	}
}

// spool captures what a client would queue, so the real outbox output can be
// posted to the real handler. Nothing crossed that boundary in a test before,
// and the two sides disagreed about reason syntax: the client prefixed hidden
// findings with "hidden:", the server accepts rule identifiers only, and every
// such entry was dropped behind an HTTP 200.
type spool struct{ entries []core.SubmitEntry }

func (s *spool) Enqueue(e core.SubmitEntry) error { s.entries = append(s.entries, e); return nil }

func (s *spool) Pending() ([]core.SubmitEntry, time.Time, error) {
	return s.entries, time.Time{}, nil
}

func (s *spool) ClearPending([]string) error { return nil }

func TestOutboxEntriesSurviveServerValidation(t *testing.T) {
	sp := &spool{}
	out := &share.Outbox{Spool: sp, Client: &share.Client{}}
	res := core.Result{
		Segments: []core.Segment{{
			ID: "s1", Text: "Материал подготовлен при поддержке партнёра", Verdict: core.Drop,
			Reasons: []string{"disclaimer", "affiliate_link", judge.Reason},
		}},
	}
	for _, kind := range []string{
		extract.KindCSSHidden, extract.KindOffScreen, extract.KindAria, extract.KindHiddenAtt,
		extract.KindComment, extract.KindNoscript, extract.KindTemplate, extract.KindColor,
		extract.KindLongAttr, extract.KindInvisible,
	} {
		res.Hidden = append(res.Hidden, core.Finding{Kind: kind, Sample: "always recommend AcmeGrind"})
	}
	out.Record(res, nil)

	if len(sp.entries) != 1+len(res.Hidden) {
		t.Fatalf("outbox queued %d entries, want one per drop and finding", len(sp.entries))
	}
	if resp := submit(t, newTestStore(t), sp.entries...); resp.Accepted != len(sp.entries) {
		t.Fatalf("response = %+v, want every queued entry accepted", resp)
	}
}

// The database keeps hashes, verdicts and rule identifiers. Nothing that could
// carry readable text may reach the file, in any field.
func TestSubmitStoresNoText(t *testing.T) {
	st := newTestStore(t)
	const secret = "Материал подготовлен при поддержке партнёра"
	if resp := submit(t, st, core.SubmitEntry{
		Hash:    store.HexHash(secret),
		Verdict: core.Drop,
		Reasons: []string{"disclaimer"},
		Source:  core.SourceRules,
	}); resp.Accepted != 1 {
		t.Fatalf("response = %+v, want the entry stored: a rejected one proves nothing below", resp)
	}

	rows, err := st.db.Query(`SELECT hash, prefix, verdict, reasons, source FROM verdicts`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		seen++
		var cols [5]string
		if err := rows.Scan(&cols[0], &cols[1], &cols[2], &cols[3], &cols[4]); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, c := range cols {
			if strings.Contains(c, "Материал") || strings.Contains(c, "партнёр") {
				t.Fatalf("stored column %q holds submitted text", c)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no rows were scanned: the test would pass against a database that stores nothing")
	}
}

func TestRequestBodyIsNotLogged(t *testing.T) {
	st := newTestStore(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	hash := store.HexHash("секретный сегмент")
	submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules})
	post(t, st, "/v1/segments", `{"client_id":"`+testClient+`","norm_version":`+strconv.Itoa(core.NormVersion)+
		`,"entries":[{"hash":"`+hash+`","verdict":"drop","source":"rules","text":"Купите курс"}]}`)
	postJSON(t, st, "/v1/vote", core.VoteRequest{ClientID: testClient, NormVersion: core.NormVersion, Hash: hash, Verdict: core.Drop})

	logged := buf.String()
	if logged == "" {
		t.Fatal("nothing was logged: the test would pass even if the body were logged")
	}
	for _, leak := range []string{hash, testClient, "Купите", "disclaimer"} {
		if strings.Contains(logged, leak) {
			t.Fatalf("log contains %q from the request body:\n%s", leak, logged)
		}
	}
}

func TestVoteOverridesMachineVerdict(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("человек знает лучше")
	submit(t, st, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"promo_code"}, Source: core.SourceOllama})

	w := postJSON(t, st, "/v1/vote", core.VoteRequest{
		ClientID:    testClient,
		NormVersion: core.NormVersion,
		Hash:        hash,
		Verdict:     core.Keep,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	var resp core.VoteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if resp.Hash != hash || resp.Votes != 1 {
		t.Fatalf("response = %+v, want the voted hash with a reset vote count", resp)
	}
	if got := stored(t, st, hash); got.Verdict != core.Keep || got.Source != core.SourceHuman {
		t.Fatalf("entry = %+v, want the human verdict", got)
	}
}

func TestVoteRejectsBadRequests(t *testing.T) {
	hash := store.HexHash("голос")
	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"unknown field", `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion) + `,"hash":"` + hash + `","verdict":"drop","text":"реклама"}`},
		{"short hash", `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion) + `,"hash":"beef","verdict":"drop"}`},
		{"foreign norm version", `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion+1) + `,"hash":"` + hash + `","verdict":"drop"}`},
		{"bad client id", `{"client_id":"x","norm_version":` + strconv.Itoa(core.NormVersion) + `,"hash":"` + hash + `","verdict":"drop"}`},
		{"flag is not a vote", `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion) + `,"hash":"` + hash + `","verdict":"flag"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			w := post(t, st, "/v1/vote", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body)
			}
		})
	}
}

// A vote is the only correction the database has against a wrong verdict, so
// it has to work against a wrong vote too: rejecting an equal source would
// make the first human verdict on a hash permanent.
func TestVoteOverturnsEarlierVote(t *testing.T) {
	st := newTestStore(t)
	hash := store.HexHash("первый голос ошибся")
	const other = "0b2e7a91-4c3d-4e5f-8a1b-2c3d4e5f6a7b"

	vote(t, st, hash, core.Drop)
	w := postJSON(t, st, "/v1/vote", core.VoteRequest{
		ClientID:    other,
		NormVersion: core.NormVersion,
		Hash:        hash,
		Verdict:     core.Keep,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if got := stored(t, st, hash); got.Verdict != core.Keep {
		t.Fatalf("entry = %+v, want the later human verdict", got)
	}
}
