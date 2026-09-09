package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"adassay.com/internal/core"
)

func reputation(t *testing.T, st *Store, clientID string) (upheld, refuted int) {
	t.Helper()
	if err := st.conn().QueryRow(`SELECT upheld, refuted FROM installs WHERE client_id = ?`, clientID).Scan(&upheld, &refuted); err != nil {
		t.Fatalf("reputation of %s: %v", clientID, err)
	}
	return upheld, refuted
}

func setAge(t *testing.T, st *Store, clientID string, age time.Duration) {
	t.Helper()
	if _, err := st.conn().Exec(`UPDATE installs SET created = ? WHERE client_id = ?`, time.Now().Add(-age).Unix(), clientID); err != nil {
		t.Fatalf("age %s: %v", clientID, err)
	}
}

func TestSidingWithAQuorumUpholdsTheInstall(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := strings.Repeat("d", 64)
	drop := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}

	for _, c := range []int{1, 2, 3} {
		if code := submitAs(t, mux, client(c), "", "", drop); code != http.StatusOK {
			t.Fatalf("client %d: status = %d", c, code)
		}
	}
	for _, c := range []int{1, 2} {
		if up, _ := reputation(t, st, client(c)); up != 0 {
			t.Errorf("client %d upheld = %d, want 0: it built the quorum rather than confirmed it", c, up)
		}
	}
	if up, _ := reputation(t, st, client(3)); up != 1 {
		t.Errorf("client 3 upheld = %d, want 1 for agreeing with a verdict already at quorum", up)
	}

	submitAs(t, mux, client(3), "", "", drop)
	if up, _ := reputation(t, st, client(3)); up != 1 {
		t.Errorf("client 3 upheld = %d after resubmitting, want 1: one hash earns once", up)
	}
}

func TestOverturnedVerdictRefutesItsBackers(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 2
	mux := testMux(st)
	hash := strings.Repeat("e", 64)
	drop := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}
	keep := core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules}

	submitAs(t, mux, client(1), "", "", drop)
	submitAs(t, mux, client(2), "", "", drop)
	submitAs(t, mux, client(3), "", "", keep)
	for _, c := range []int{1, 2} {
		if _, ref := reputation(t, st, client(c)); ref != 0 {
			t.Errorf("client %d refuted = %d before the challenger had a quorum", c, ref)
		}
	}
	submitAs(t, mux, client(4), "", "", keep)
	if got := stored(t, st, hash); got.Verdict != core.Keep {
		t.Fatalf("verdict = %v, want the challengers' keep at quorum", got.Verdict)
	}
	for _, c := range []int{1, 2} {
		if _, ref := reputation(t, st, client(c)); ref != 1 {
			t.Errorf("client %d refuted = %d, want 1 once its verdict was overturned", c, ref)
		}
	}
	for _, c := range []int{3, 4} {
		if _, ref := reputation(t, st, client(c)); ref != 0 {
			t.Errorf("client %d refuted = %d, want 0 for the side that won", c, ref)
		}
	}
}

func TestContradictingASettledVerdictRefutesTheInstall(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 1
	mux := testMux(st)
	hash := strings.Repeat("f", 64)

	voteAs(t, mux, client(1), hash, core.Drop)
	keep := core.SubmitEntry{Hash: hash, Verdict: core.Keep, Source: core.SourceRules}
	for range 2 {
		submitAs(t, mux, client(2), "", "", keep)
	}
	if _, ref := reputation(t, st, client(2)); ref != 1 {
		t.Errorf("client 2 refuted = %d, want exactly 1: a repeated contradiction is one contribution", ref)
	}
	if _, ref := reputation(t, st, client(1)); ref != 0 {
		t.Errorf("client 1 refuted = %d, want 0 for holding the settled verdict", ref)
	}
}

func TestRankGuardCountsLiveBacking(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("9", 64)

	// Three humans vote while their installs are too young to weigh anything,
	// so the stored count stays at zero; a day later they are a quorum.
	for _, c := range []int{1, 2, 3} {
		setAge(t, st, client(c), time.Hour)
		if code := voteAs(t, mux, client(c), hash, core.Keep); code != http.StatusOK {
			t.Fatalf("vote %d: status = %d", c, code)
		}
	}
	var votes int
	if err := st.conn().QueryRow(`SELECT votes FROM verdicts WHERE hash = ?`, hash).Scan(&votes); err != nil || votes != 0 {
		t.Fatalf("votes = %d (%v), want 0 while every backer is fresh", votes, err)
	}
	if published(t, st, hash) {
		t.Fatal("fresh backers must not publish")
	}
	for _, c := range []int{1, 2, 3} {
		setAge(t, st, client(c), 2*installMinAge)
	}
	if !published(t, st, hash) {
		t.Fatal("aged backers must publish the human verdict")
	}

	drop := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Source: core.SourceRules}
	for _, c := range []int{4, 5, 6} {
		submitAs(t, mux, client(c), "", "", drop)
	}
	got := stored(t, st, hash)
	if got.Verdict != core.Keep || got.Source != core.SourceHuman {
		t.Fatalf("entry = %+v, want the human quorum to stand against lower-ranked machines", got)
	}
}
