package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/belaytzev/adfilter/internal/core"
	"github.com/belaytzev/adfilter/internal/store"
)

// published reports whether the bucket endpoint serves a verdict for hash,
// which is the only view a client ever gets of the database.
func published(t *testing.T, st *Store, hash string) bool {
	t.Helper()
	for _, e := range decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))).Entries {
		if e.Hash == hash {
			return true
		}
	}
	return false
}

func submitAs(t *testing.T, mux http.Handler, clientID, remoteAddr, forwarded string, entries ...core.SubmitEntry) int {
	t.Helper()
	body, err := json.Marshal(core.SubmitRequest{ClientID: clientID, NormVersion: core.NormVersion, Entries: entries})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/segments", strings.NewReader(string(body)))
	if remoteAddr != "" {
		r.RemoteAddr = remoteAddr
	}
	if forwarded != "" {
		r.Header.Set("CF-Connecting-IP", forwarded)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code
}

func voteAs(t *testing.T, mux http.Handler, clientID, hash string, verdict core.Verdict) int {
	t.Helper()
	body, err := json.Marshal(core.VoteRequest{ClientID: clientID, NormVersion: core.NormVersion, Hash: hash, Verdict: verdict})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/vote", strings.NewReader(string(body))))
	return w.Code
}

func client(n int) string { return fmt.Sprintf("6f1c9f4e-2b8a-4c1d-9f3e-0a7b5c2d8e%02d", n) }

func TestVerdictLeavesQuarantineOnQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := store.HexHash("Материал подготовлен при поддержке")
	entry := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules}

	for i := 1; i <= st.quorum; i++ {
		if code := submitAs(t, mux, client(i), "", "", entry); code != http.StatusOK {
			t.Fatalf("submit %d: status = %d, want 200", i, code)
		}
		want := i >= st.quorum
		if got := published(t, st, hash); got != want {
			t.Fatalf("after %d confirmations published = %v, want %v", i, got, want)
		}
	}
}

// The attack the quarantine exists for: one installation inventing verdicts at
// volume. Repetition is not agreement, so nothing it says reaches other clients.
func TestFloodFromOneClientPublishesNothing(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)

	var hashes []string
	var entries []core.SubmitEntry
	for i := 0; i < 50; i++ {
		hash := store.HexHash(fmt.Sprintf("совершенно обычный абзац %d", i))
		hashes = append(hashes, hash)
		entries = append(entries, core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})
	}
	// The cheap attack is the batch endpoint, not one request per verdict:
	// three calls are enough to claim fifty segments are advertising.
	for range 3 {
		if code := submitAs(t, mux, client(1), "", "", entries...); code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
	}
	for _, hash := range hashes {
		if published(t, st, hash) {
			t.Fatalf("hash %q was published on one client's word alone", hash)
		}
	}
}

// verdictOf reports the verdict the bucket endpoint serves for hash.
func verdictOf(t *testing.T, st *Store, hash string) (core.Verdict, bool) {
	t.Helper()
	for _, e := range decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))).Entries {
		if e.Hash == hash {
			return e.Verdict, true
		}
	}
	return core.Keep, false
}

// A challenger takes the row only once it holds a quorum of its own. Until then
// the verdict clients already agreed on keeps being served — a contradiction is
// one client's opinion, and one opinion may not decide what everybody reads.
func TestOverturningTakesItsOwnQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := store.HexHash("спорный абзац")

	for i := 1; i <= 2; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
	}
	if v, ok := verdictOf(t, st, hash); !ok || v != core.Keep {
		t.Fatalf("verdict with a quorum: %v, %v; want keep served", v, ok)
	}

	voteAs(t, mux, client(9), hash, core.Drop)
	if v, ok := verdictOf(t, st, hash); !ok || v != core.Keep {
		t.Fatalf("after one contradicting vote: %v, %v; want the confirmed keep still served", v, ok)
	}
	voteAs(t, mux, client(8), hash, core.Drop)
	if v, ok := verdictOf(t, st, hash); !ok || v != core.Drop {
		t.Fatalf("after a quorum of votes: %v, %v; want drop served", v, ok)
	}
}

// The unpublish attack: hashes are handed out in full by the open bucket
// endpoint, so contradicting them one by one must not empty the database.
// Neither a human vote nor a higher-ranked source may retire a confirmed
// verdict alone, and repeating itself under fresh identities is the one thing
// a single client cannot do.
func TestOneClientCannotUnpublishAVerdict(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := store.HexHash("абзац, который решили выкинуть")

	for i := 1; i <= 3; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules})
	}
	if !published(t, st, hash) {
		t.Fatal("verdict with a quorum is not served")
	}

	for range 20 {
		voteAs(t, mux, client(9), hash, core.Keep)
		submitAs(t, mux, client(9), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceOllama})
	}
	v, ok := verdictOf(t, st, hash)
	if !ok {
		t.Fatal("one client unpublished a verdict three others confirmed")
	}
	if v != core.Drop {
		t.Fatalf("served verdict = %v, want drop: one client rewrote what everybody reads", v)
	}
}

// votes is what a client reads as "how many people agree". It counts distinct
// clients, so resending the same verdict — an outbox retry, or a deliberate
// flood — leaves it where it was.
func TestVotesCountClientsNotRequests(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := store.HexHash("абзац с промокодом")
	entry := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}

	submitAs(t, mux, client(1), "", "", entry)
	for range 10 {
		submitAs(t, mux, client(2), "", "", entry)
	}
	entries := decodeBucket(t, get(t, st, bucketPath(hash[:core.PrefixLen], core.NormVersion))).Entries
	for _, e := range entries {
		if e.Hash == hash && e.Votes != 2 {
			t.Fatalf("votes = %d, want 2: ten resends from one client are not ten agreements", e.Votes)
		}
	}
}

func TestWritesAreRateLimited(t *testing.T) {
	st := newTestStore(t)
	mux := testMux(st)
	entry := core.SubmitEntry{Hash: store.HexHash("любой сегмент"), Verdict: core.Drop, Source: core.SourceRules}

	for i := 0; i < writeBurst; i++ {
		if code := submitAs(t, mux, client(1), "203.0.113.7:4000", "", entry); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
	}
	if code := submitAs(t, mux, client(1), "203.0.113.7:4000", "", entry); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once the burst is spent", code)
	}
	// The limit is per address, not global: another client is unaffected.
	if code := submitAs(t, mux, client(2), "203.0.113.8:4000", "", entry); code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a different address", code)
	}
	// Reads are never limited: a blocked lookup would push clients back onto
	// their own heuristics, which is the failure the database exists to fix.
	if w := get(t, st, bucketPath(entry.Hash[:core.PrefixLen], core.NormVersion)); w.Code != http.StatusOK {
		t.Fatalf("bucket status = %d, want 200", w.Code)
	}
}

// The limit is on verdicts, not on requests: a batch carries up to maxBatch of
// them, so charging one token per request would let a flood write maxBatch
// times the rate the constant promises.
func TestBatchIsChargedPerVerdict(t *testing.T) {
	st := newTestStore(t)
	mux := testMux(st)

	var entries []core.SubmitEntry
	for i := range writeBurst {
		entries = append(entries, core.SubmitEntry{
			Hash:    store.HexHash(fmt.Sprintf("сегмент %d", i)),
			Verdict: core.Drop,
			Source:  core.SourceRules,
		})
	}
	if code := submitAs(t, mux, client(1), "203.0.113.9:4000", "", entries...); code != http.StatusOK {
		t.Fatalf("status = %d, want 200: one full batch fits the burst", code)
	}
	if code := submitAs(t, mux, client(1), "203.0.113.9:4000", "", entries[0]); code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: the batch spent the whole burst", code)
	}
}

// A forwarded-for header is trustworthy only from a proxy we run. From anyone
// else it is attacker-controlled: believing it lets one host mint a fresh
// identity, and a fresh rate limit, for every request it sends.
func TestForgedForwardedIPIsIgnored(t *testing.T) {
	st := newTestStore(t)
	entry := core.SubmitEntry{Hash: store.HexHash("любой сегмент"), Verdict: core.Drop, Source: core.SourceRules}

	untrusted, err := newGuard("192.0.2.10")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	mux := newMux(st, untrusted, &metrics{})
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", fmt.Sprintf("198.51.100.%d", i), entry)
		if i < writeBurst && code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
		if i == writeBurst && code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429: a forged CF-Connecting-IP reset the limit", code)
		}
	}

	trusted, err := newGuard("203.0.113.0/24")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	mux = newMux(st, trusted, &metrics{})
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", fmt.Sprintf("198.51.100.%d", i), entry)
		if code != http.StatusOK {
			t.Fatalf("request %d from a trusted proxy: status = %d, want 200", i, code)
		}
	}
	for i := 0; i <= writeBurst; i++ {
		code := submitAs(t, mux, client(1), "203.0.113.7:4000", "198.51.100.200", entry)
		if i == writeBurst && code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 for one client behind a trusted proxy", code)
		}
	}
}

func TestParseTrustedRejectsGarbage(t *testing.T) {
	if _, err := newGuard("not-an-address"); err == nil {
		t.Fatal("newGuard accepted a proxy list that is neither an address nor a CIDR")
	}
	g, err := newGuard(" 203.0.113.7 , 2001:db8::/32 ,")
	if err != nil {
		t.Fatalf("newGuard: %v", err)
	}
	if len(g.trusted) != 2 {
		t.Fatalf("trusted = %v, want two prefixes", g.trusted)
	}
}

// A write limit keyed on a /128 limits nothing: the smallest IPv6 allocation a
// subscriber gets is a /64, so the whole prefix has to share one bucket.
func TestClientIPGroupsIPv6BySubnet(t *testing.T) {
	g, err := newGuard("")
	if err != nil {
		t.Fatal(err)
	}
	key := func(host string) string {
		return clientIP(&http.Request{RemoteAddr: host}, g.trusted)
	}
	first, second := key("[2001:db8:1:2::1]:443"), key("[2001:db8:1:2:ffff::9]:443")
	if first != second {
		t.Errorf("two addresses of one /64 got separate buckets: %q and %q", first, second)
	}
	if other := key("[2001:db8:1:3::1]:443"); other == first {
		t.Errorf("two different /64s share a bucket: %q", other)
	}
	if v4 := key("192.0.2.7:443"); v4 != "192.0.2.7" {
		t.Errorf("IPv4 key = %q, want the address itself", v4)
	}
}

// A flood from many addresses must not grow the limiter without a bound: every
// bucket is mid-spend, so pruning full ones frees nothing and the map would
// keep growing while each new address pays for an O(n) scan.
func TestLimiterStaysBounded(t *testing.T) {
	l := newLimiter(writesPerSecond, writeBurst)
	for i := range maxTrackedIPs * 2 {
		l.allow(fmt.Sprintf("192.0.2.%d:%d", i%256, i))
	}
	if len(l.seen) > maxTrackedIPs {
		t.Fatalf("tracked addresses = %d, want at most %d", len(l.seen), maxTrackedIPs)
	}
}

// A vote and an outbox flush are separate processes on one installation: the
// flush can read a stale rules row before the vote and post it afterwards.
// Confirmations are one per client, so without a source guard that late row
// would take the reader's correction back.
func TestStaleBatchCannotRetractAVote(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := store.HexHash("реклама, которую читатель оправдал")

	for i := 1; i <= 2; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})
	}
	voteAs(t, mux, client(1), hash, core.Keep)
	submitAs(t, mux, client(1), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules})

	if n, err := backers(st.db, hash, core.NormVersion, core.Keep.String()); err != nil || n != 1 {
		t.Fatalf("keep backers = %d (%v), want the vote still standing", n, err)
	}
	if n, err := backers(st.db, hash, core.NormVersion, core.Drop.String()); err != nil || n != 1 {
		t.Fatalf("drop backers = %d (%v), want only the client that did not vote", n, err)
	}
}

// A database written before source_rank existed holds no source for its
// confirmations. Left at the column default they rank below every incoming
// batch, so the first stale rules flush after the upgrade would retract a vote
// cast before it — the very move the guard above exists to refuse.
func TestUpgradeKeepsOldConfirmationsOutOfReachOfBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	hash := store.HexHash("сегмент из старой базы")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE confirmations (
		hash TEXT NOT NULL, norm_version INTEGER NOT NULL, client_id TEXT NOT NULL,
		verdict TEXT NOT NULL, created INTEGER NOT NULL,
		PRIMARY KEY (hash, norm_version, client_id));
		INSERT INTO confirmations VALUES (?, ?, ?, ?, 0)`,
		hash, core.NormVersion, client(1), core.Keep.String()); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	db.Close()

	st, err := openStore(path)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := confirm(st.db, hash, core.NormVersion, client(1), core.Drop.String(), core.SourceRules); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if n, err := backers(st.db, hash, core.NormVersion, core.Keep.String()); err != nil || n != 1 {
		t.Fatalf("keep backers = %d (%v), want the pre-upgrade confirmation still standing", n, err)
	}
}

// Agreement adds a backer and nothing else. A row's source decides who is
// allowed to overturn it, so if one agreeing client could relabel a published
// rules verdict as ollama, that client would have handed itself a veto: every
// honest correction after it arrives as "rules" and would be turned away as
// weaker than the label the attacker wrote.
func TestAgreementCannotRelabelTheSource(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := store.HexHash("абзац с промокодом")

	for i := 1; i <= 3; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"promo_code"}, Source: core.SourceRules})
	}
	submitAs(t, mux, client(9), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceOllama})

	if e := stored(t, st, hash); e.Source != core.SourceRules {
		t.Fatalf("source = %q after one agreeing client, want it left at rules", e.Source)
	}
	for i := 4; i <= 6; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
	}
	if v, ok := verdictOf(t, st, hash); !ok || v != core.Keep {
		t.Fatalf("verdict = %v, %v; want the honest quorum's keep, not a row frozen by a claimed source", v, ok)
	}
}

// The rank rule guards a verdict the crowd backs, not a claim one stranger
// filed. A hash nobody else has confirmed yet belongs to no one: were a
// stronger source enough to hold it, whoever wrote it first would keep it
// against every client that later derived the opposite.
func TestUnconfirmedRowDoesNotOutrankAQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := store.HexHash("абзац, который никто ещё не видел")

	submitAs(t, mux, client(9), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceOllama})
	if published(t, st, hash) {
		t.Fatal("one client's verdict is served without a quorum")
	}
	for i := 1; i <= 3; i++ {
		submitAs(t, mux, client(i), "", "", core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules})
	}
	if v, ok := verdictOf(t, st, hash); !ok || v != core.Keep {
		t.Fatalf("verdict = %v, %v; want keep: three clients derived it against one unconfirmed claim", v, ok)
	}
}
