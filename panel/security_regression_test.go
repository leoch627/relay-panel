package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testCallAs(t *testing.T, mux *http.ServeMux, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func setTestPassword(t *testing.T, s *Store, id int64, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range s.st.Users {
		if u.ID == id {
			u.PasswordHash = string(hash)
			return
		}
	}
	t.Fatalf("user %d not found", id)
}

func TestImportReportsPlainValidationError(t *testing.T) {
	a, mux, _ := testAPI(t)
	good := testRule("good_import", 1)
	bad := testRule("bad tag!", 1)
	bad.ListenPort++
	w := testCall(t, mux, "POST", "/api/rules/import", map[string]any{"rules": []*Rule{good, bad}})
	var res struct {
		Imported int    `json:"imported"`
		Failed   int    `json:"failed"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || res.Imported != 1 || res.Failed != 1 || a.store.GetRule("good_import") == nil {
		t.Fatalf("import result: %d %s", w.Code, w.Body.String())
	}
	if want := "bad tag!：标签只能包含字母数字下划线横杠，长度 1-32"; res.Error != want {
		t.Fatalf("error = %q, want %q", res.Error, want)
	}
}

func TestNodeListHidesOtherUsersRules(t *testing.T) {
	a, mux, _ := testAPI(t)
	a.store.st.Sessions["test-alice"] = Session{UserID: 2, ExpiresAt: time.Now().Add(time.Hour)}
	n, err := a.store.CreateNode("shared")
	if err != nil {
		t.Fatal(err)
	}
	for i, owner := range []int64{2, 3} {
		r := testRule(fmt.Sprintf("rule_of_%d", owner), owner)
		r.NodeID = n.ID
		r.ListenPort += i
		if err := a.store.UpsertRule(r); err != nil {
			t.Fatal(err)
		}
	}
	testNodeCall(t, mux, n.Key, "/api/node/report", map[string]any{"statuses": map[string]UnitStatus{
		"rule_of_2": {Active: "active"}, "rule_of_3": {Active: "active"},
	}})

	type nodeRow struct {
		Key       *string `json:"key"`
		RuleCount int     `json:"rule_count"`
		Report    struct {
			Statuses map[string]UnitStatus `json:"statuses"`
		} `json:"report"`
	}
	list := func(token string) nodeRow {
		t.Helper()
		w := testCallAs(t, mux, token, "GET", "/api/nodes", nil)
		var out struct{ Nodes []nodeRow }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || len(out.Nodes) != 1 {
			t.Fatalf("list as %s: %d %s", token, w.Code, w.Body.String())
		}
		return out.Nodes[0]
	}

	alice := list("test-alice")
	if _, leaked := alice.Report.Statuses["rule_of_3"]; leaked || alice.Report.Statuses["rule_of_2"].Active != "active" {
		t.Fatalf("non-admin statuses = %v, want only rule_of_2", alice.Report.Statuses)
	}
	if alice.RuleCount != 1 || alice.Key != nil {
		t.Fatalf("non-admin row leaked counts or key: count=%d key=%v", alice.RuleCount, alice.Key)
	}
	admin := list("test-admin")
	if len(admin.Report.Statuses) != 2 || admin.RuleCount != 2 || admin.Key == nil {
		t.Fatalf("admin row incomplete: %+v", admin)
	}
}

func TestSlaveIgnoresTraversalTags(t *testing.T) {
	s, st, m := newSlaveTest(t)
	conf := filepath.Join(s.confDir, "conf")
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	s.confDir = conf
	pr := &pullResponse{Rules: []nodeRulePayload{
		{Tag: "../escape", Revision: 1, Desired: "running", Env: "REMOTE_IP=127.0.0.1\n"},
		{Tag: "good", Revision: 1, Desired: "running", Env: "REMOTE_IP=127.0.0.1\n"},
	}}
	pr.Rules = validPulledRules(pr.Rules)
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	if _, err := os.Stat(filepath.Join(conf, "..", "escape.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("traversal tag wrote outside conf dir: %v", err)
	}
	if _, ok := st.Applied["../escape"]; ok || m.running["../escape"] {
		t.Fatal("traversal tag was applied")
	}
	if _, err := os.Stat(filepath.Join(conf, "good.conf")); err != nil || !m.running["good"] {
		t.Fatalf("valid rule not applied: %v", err)
	}
}

func TestSlaveStateDropsInvalidTags(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	persisted := slaveState{
		Applied:     map[string]int64{"../x": 1, "ok": 2},
		QuotaStops:  map[string]quotaStop{"../x": {Bytes: 1}},
		QuotaResets: map[string]int64{"a/b": 3},
		Rules:       []nodeRulePayload{{Tag: "../x"}, {Tag: "ok"}},
	}
	data, _ := json.Marshal(&persisted)
	if err := os.WriteFile(s.statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	st := s.loadState()
	if len(st.Applied) != 1 || st.Applied["ok"] != 2 || len(st.QuotaStops) != 0 || len(st.QuotaResets) != 0 {
		t.Fatalf("invalid tags survived load: %+v", st)
	}
	if len(st.Rules) != 1 || st.Rules[0].Tag != "ok" {
		t.Fatalf("invalid rules survived load: %+v", st.Rules)
	}
}

func TestLoginLockoutIsPerClient(t *testing.T) {
	a, mux, _ := testAPI(t)
	setTestPassword(t, a.store, 1, "admin-secret")
	attacker, owner := "203.0.113.9", "198.51.100.7"
	for i := 0; i < loginPairLimit; i++ {
		if _, _, err := a.store.Login("admin", "wrong", attacker); !errors.Is(err, ErrBadAuth) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, _, err := a.store.Login("admin", "admin-secret", attacker); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("attacker not locked: %v", err)
	}
	if _, _, err := a.store.Login("admin", "admin-secret", owner); err != nil {
		t.Fatalf("attacker locked the owner out: %v", err)
	}

	// The HTTP handler surfaces lockouts as 429. httptest requests come from 192.0.2.1.
	for i := 0; i < loginPairLimit; i++ {
		testCall(t, mux, "POST", "/api/login", map[string]string{"username": "alice", "password": "wrong"})
	}
	if w := testCall(t, mux, "POST", "/api/login", map[string]string{"username": "alice", "password": "wrong"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login: HTTP %d %s", w.Code, w.Body.String())
	}
}

func TestLoginSprayLocksClient(t *testing.T) {
	a, _, _ := testAPI(t)
	for i := int64(4); i <= 8; i++ {
		a.store.st.Users = append(a.store.st.Users, &User{ID: i, Username: fmt.Sprintf("user%d", i)})
		setTestPassword(t, a.store, i, "pw")
	}
	sprayer := "203.0.113.20"
	for i := int64(4); i <= 7; i++ {
		for j := 0; j < loginPairLimit; j++ {
			a.store.Login(fmt.Sprintf("user%d", i), "wrong", sprayer)
		}
	}
	if _, _, err := a.store.Login("user8", "pw", sprayer); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("spraying client not locked: %v", err)
	}
	if _, _, err := a.store.Login("user8", "pw", "198.51.100.30"); err != nil {
		t.Fatalf("other client affected: %v", err)
	}
}

func TestLoginDoesNotRevealAccountState(t *testing.T) {
	a, _, _ := testAPI(t)
	setTestPassword(t, a.store, 2, "alice-secret")
	a.store.st.Users[1].Disabled = true
	ip := "203.0.113.40"
	if _, _, err := a.store.Login("ghost", "x", ip); !errors.Is(err, ErrBadAuth) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, _, err := a.store.Login("alice", "wrong", ip); !errors.Is(err, ErrBadAuth) {
		t.Fatalf("disabled user with wrong password revealed state: %v", err)
	}
	if _, _, err := a.store.Login("alice", "alice-secret", ip); err == nil || !strings.Contains(err.Error(), "禁用") {
		t.Fatalf("disabled user with correct password: %v", err)
	}
	// Unknown usernames lock exactly like real ones, so lockouts do not confirm existence.
	for i := 1; i < loginPairLimit; i++ {
		a.store.Login("ghost", "x", ip)
	}
	if _, _, err := a.store.Login("ghost", "x", ip); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("unknown username never locks: %v", err)
	}
}

func TestLoginClientIPTrustsOnlyLocalProxies(t *testing.T) {
	cases := []struct {
		remote  string
		headers map[string]string
		want    string
	}{
		{"127.0.0.1:4000", map[string]string{"X-Forwarded-For": "1.1.1.1, 203.0.113.5"}, "203.0.113.5"},
		{"10.0.0.2:4000", map[string]string{"X-Real-IP": "203.0.113.8"}, "203.0.113.8"},
		{"198.51.100.1:4000", map[string]string{"X-Forwarded-For": "10.0.0.1"}, "198.51.100.1"},
		{"[::1]:4000", map[string]string{"X-Forwarded-For": "not-an-ip"}, "::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.RemoteAddr = c.remote
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		if got := loginClientIP(r); got != c.want {
			t.Errorf("loginClientIP(%s, %v) = %q, want %q", c.remote, c.headers, got, c.want)
		}
	}
}
