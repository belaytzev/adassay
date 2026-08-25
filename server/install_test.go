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

func TestSeedPublishesWhenItAgreesWithAnExistingVerdict(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("d", 64)

	first := client(21)
	seedInstall(t, st, first)
	if code := submitAs(t, mux, first, "", "", core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceRules,
	}); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if published(t, st, hash) {
		t.Fatal("one ordinary install is below quorum, so the verdict must still be quarantined")
	}

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	resp := submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})
	if resp.Accepted != 1 {
		t.Fatalf("response = %+v, want the seeded verdict accepted", resp)
	}
	if !published(t, st, hash) {
		t.Fatal("a seeder that agrees with a quarantined verdict must publish it: agreeing with " +
			"someone else is the common case once the database has users, not the rare one")
	}
	if got := stored(t, st, hash); got.Source != core.SourceRules {
		t.Errorf("source = %q, want %q: publication is its own fact, so vouching for a verdict must "+
			"not rewrite who decided it", got.Source, core.SourceRules)
	}
}

func TestSeedGrantsNoRankProtectionToAnEarlierVerdict(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("e", 64)

	first := client(22)
	seedInstall(t, st, first)
	if code := submitAs(t, mux, first, "", "", core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"model_judgement"}, Source: core.SourceOllama,
	}); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})

	challenger := client(23)
	seedInstall(t, st, challenger)
	if code := submitAs(t, mux, challenger, "", "", core.SubmitEntry{
		Hash: hash, Verdict: core.Keep, Source: core.SourceRules,
	}); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := stored(t, st, hash).Votes; got >= st.quorum {
		t.Errorf("votes = %d, want below quorum %d: one seeder agreeing is one backer, and storing "+
			"a synthetic quorum locks out every later challenger by rank", got, st.quorum)
	}
}

func TestSeedDoesNotDemoteAHumanVerdict(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("f", 64)

	for i := 31; i < 34; i++ {
		seedInstall(t, st, client(i))
		if code := voteAs(t, mux, client(i), hash, core.Drop); code != http.StatusOK {
			t.Fatalf("vote %d: status = %d, want 200", i, code)
		}
	}
	if got := stored(t, st, hash); got.Source != core.SourceHuman {
		t.Fatalf("source = %q, want %q before the seeder arrives", got.Source, core.SourceHuman)
	}

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})

	if got := stored(t, st, hash); got.Source != core.SourceHuman {
		t.Errorf("source = %q, want %q: agreeing with people must not take their decision's "+
			"provenance, nor the rank guard that protects it", got.Source, core.SourceHuman)
	}
}

func TestSeedPublishesASubQuorumHumanVerdict(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("1", 64)

	voter := client(41)
	seedInstall(t, st, voter)
	if code := voteAs(t, mux, voter, hash, core.Drop); code != http.StatusOK {
		t.Fatalf("vote: status = %d, want 200", code)
	}
	if published(t, st, hash) {
		t.Fatal("one vote is below quorum, so the verdict must still be quarantined")
	}

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})

	if !published(t, st, hash) {
		t.Error("a single vote creates a human row with no quorum behind it, so treating every human " +
			"row as already published leaves it quarantined for good")
	}
	if got := stored(t, st, hash); got.Source != core.SourceHuman {
		t.Errorf("source = %q, want %q: publishing it must not take the provenance", got.Source, core.SourceHuman)
	}
}

func TestPublicationSurvivesAnOrdinaryAgreement(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("2", 64)
	entry := core.SubmitEntry{Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}}

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	seeded := entry
	seeded.Source = core.SourceSeed
	submit(t, st, seeded)
	if !published(t, st, hash) {
		t.Fatal("the seeded verdict was not published")
	}

	later := client(42)
	seedInstall(t, st, later)
	agreeing := entry
	agreeing.Source = core.SourceRules
	if code := submitAs(t, mux, later, "", "", agreeing); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if !published(t, st, hash) {
		t.Error("someone agreeing with a published verdict must not unpublish it: the row is " +
			"rewritten on every submit, so the flag has to be carried rather than defaulted")
	}
}

func TestChangingTheVerdictDropsPublication(t *testing.T) {
	st := newTestStore(t)
	st.quorum = 3
	mux := testMux(st)
	hash := strings.Repeat("3", 64)

	seedInstall(t, st, testClient)
	markSeeder(t, st, testClient)
	submit(t, st, core.SubmitEntry{
		Hash: hash, Verdict: core.Drop, Reasons: []string{"disclaimer"}, Source: core.SourceSeed,
	})
	if !published(t, st, hash) {
		t.Fatal("the seeded verdict was not published")
	}

	// One reputable install weighs as much as the quorum on its own, so it can
	// carry a different verdict all the way to the upsert with a single
	// confirmation behind it.
	challenger := client(43)
	seedInstall(t, st, challenger)
	if _, err := st.conn().Exec(`UPDATE installs SET upheld = 2 WHERE client_id = ?`, challenger); err != nil {
		t.Fatalf("upheld: %v", err)
	}
	if code := submitAs(t, mux, challenger, "", "", core.SubmitEntry{
		Hash: hash, Verdict: core.Keep, Source: core.SourceRules,
	}); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	if published(t, st, hash) {
		t.Error("an earlier vouch was for the earlier answer: carrying publication onto a verdict " +
			"that replaced it serves one install's opinion to everyone")
	}
}
