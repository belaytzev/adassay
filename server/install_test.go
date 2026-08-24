package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"adassay.com/internal/core"
)

func TestUnregisteredInstallCannotWrite(t *testing.T) {
	st := newTestStore(t)
	body := `{"client_id":"` + testClient + `","norm_version":` + strconv.Itoa(core.NormVersion) + `,"entries":[{"hash":"` + strings.Repeat("a", 64) + `","verdict":"drop","source":"rules"}]}`

	r := httptest.NewRequest(http.MethodPost, "/v1/segments", strings.NewReader(body))
	r.Header.Set(headerInstall, testClient)
	r.Header.Set(headerSecret, strings.Repeat("b", 64))
	w := httptest.NewRecorder()
	testMux(st).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a wrong secret must not write", w.Code)
	}
}

func TestRegisterIssuesUsableCredentials(t *testing.T) {
	st := newTestStore(t)
	w := httptest.NewRecorder()
	testMux(st).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/register", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200", w.Code)
	}
	var out core.RegisterResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ClientID == "" || out.Secret == "" {
		t.Fatal("register returned empty credentials")
	}
	in, err := loadInstall(st.conn(), out.ClientID)
	if err != nil {
		t.Fatalf("load install: %v", err)
	}
	if !in.verify(out.Secret) {
		t.Error("the issued secret does not verify against what was stored")
	}
	if in.verify(strings.Repeat("c", 64)) {
		t.Error("an unrelated secret verified")
	}
}

func TestFreshInstallCarriesNoWeight(t *testing.T) {
	now := time.Now()
	fresh := install{created: now.Add(-time.Hour)}
	if w := fresh.weight(now); w != 0 {
		t.Errorf("weight = %d, want 0: a day-old install must not count toward quorum", w)
	}
	aged := install{created: now.Add(-30 * 24 * time.Hour)}
	if w := aged.weight(now); w != 1 {
		t.Errorf("weight = %d, want 1", w)
	}
	trusted := install{created: now.Add(-30 * 24 * time.Hour), upheld: 10}
	if w := trusted.weight(now); w != 1+installMaxWeit {
		t.Errorf("weight = %d, want %d: contribution is capped", w, 1+installMaxWeit)
	}
	harmful := install{created: now.Add(-30 * 24 * time.Hour), upheld: 1, refuted: 5}
	if w := harmful.weight(now); w != 0 {
		t.Errorf("weight = %d, want 0: refuted contributions must sink the weight", w)
	}
}

func TestLimiterRefusesNewAddressesWhenFull(t *testing.T) {
	l := newLimiter(1, 2)
	for i := range maxTrackedIPs {
		if !l.allow(strconv.Itoa(i)) {
			t.Fatalf("address %d refused while there was room", i)
		}
	}
	if l.allow("one-too-many") {
		t.Error("a new address was admitted past the cap: the table must fill, not reset")
	}
	if !l.allow("0") {
		t.Error("an address already tracked lost its budget when the table filled")
	}
}

func markSeeder(t *testing.T, st *Store, clientID string) {
	t.Helper()
	if _, err := st.conn().Exec(`UPDATE installs SET seeder = 1 WHERE client_id = ?`, clientID); err != nil {
		t.Fatalf("mark seeder: %v", err)
	}
}

func TestSeedPublishesWithoutQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	hash := strings.Repeat("a", 64)
	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)

	resp := submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})
	if resp.Accepted != 1 {
		t.Fatalf("response = %+v, want the seeded verdict accepted", resp)
	}
	if !published(t, st, hash) {
		t.Error("a seeded verdict must be served immediately: bootstrapping cannot wait for a quorum " +
			"that only exists once there are users")
	}
}

func TestSeedFromAnOrdinaryInstallIsRejected(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	hash := strings.Repeat("b", 64)

	resp := submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})
	if resp.Rejected != 1 {
		t.Fatalf("response = %+v, want the entry rejected: seeding bypasses quorum, so it cannot be "+
			"self-granted through the API", resp)
	}
	if published(t, st, hash) {
		t.Error("an unprivileged install published a verdict without quorum")
	}
}

func TestSeedYieldsToHumanQuorum(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	hash := strings.Repeat("c", 64)
	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)

	submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})
	if got := stored(t, st, hash); got.Verdict != core.Drop {
		t.Fatalf("verdict = %v, want the seeded drop to stand before anyone objects", got.Verdict)
	}

	mux := testMux(st)
	for i := 7; i < 10; i++ {
		if code := voteAs(t, mux, client(i), hash, core.Keep); code != http.StatusOK {
			t.Fatalf("vote %d: status = %d, want 200", i, code)
		}
	}

	if got := stored(t, st, hash); got.Verdict != core.Keep {
		t.Errorf("verdict = %v, want keep: seeded data carries no rank protection against people, "+
			"so a quorum of human votes must be able to correct it", got.Verdict)
	}
}

func TestMarkSeedersGrantsAndRevokes(t *testing.T) {
	st := newTestStore(t)
	seedInstall(t, st, testClient)
	other := "00000000-0000-0000-0000-0000000000ff"
	seedInstall(t, st, other)

	if err := st.markSeeders([]string{" " + testClient + " ", "", "no-such-install"}); err != nil {
		t.Fatalf("markSeeders: %v", err)
	}
	in, err := loadInstall(st.conn(), testClient)
	if err != nil || !in.seeder {
		t.Fatalf("loadInstall(%s).seeder = %v, %v; want true", testClient, in.seeder, err)
	}

	if err := st.markSeeders([]string{other}); err != nil {
		t.Fatalf("markSeeders: %v", err)
	}
	in, err = loadInstall(st.conn(), testClient)
	if err != nil || in.seeder {
		t.Errorf("dropping an id from the list must revoke it, seeder = %v, %v", in.seeder, err)
	}
	in, err = loadInstall(st.conn(), other)
	if err != nil || !in.seeder {
		t.Errorf("loadInstall(%s).seeder = %v, %v; want true", other, in.seeder, err)
	}
}
