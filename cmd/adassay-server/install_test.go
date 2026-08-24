package main

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
	in, err := loadInstall(st.db, out.ClientID)
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
