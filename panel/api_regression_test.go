package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testAPI(t *testing.T) (*API, *http.ServeMux, string) {
	t.Helper()
	d := t.TempDir()
	s, err := LoadStore(filepath.Join(d, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.st.Users = []*User{{ID: 1, Username: "admin", IsAdmin: true}, {ID: 2, Username: "alice"}, {ID: 3, Username: "bob"}}
	s.st.Sessions["test-admin"] = Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}
	paths := DefaultPaths()
	paths.ConfDir = filepath.Join(d, "conf")
	paths.RelaysConf = filepath.Join(d, "conf", "relays.conf")
	log := filepath.Join(d, "commands.log")
	if err := os.WriteFile(filepath.Join(d, "systemctl"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TEST_COMMAND_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_COMMAND_LOG", log)
	run := &Runner{}
	a := &API{store: s, run: run, sd: NewSystemdManager(run, paths)}
	mux := http.NewServeMux()
	a.Register(mux)
	return a, mux, log
}
func testRule(tag string, owner int64) *Rule {
	return &Rule{Tag: tag, OwnerID: owner, ListenIP: "127.0.0.1", ListenPort: 28001, RemoteHost: "127.0.0.1", RemoteTCPPort: 28002, EnableTCP: true, DesiredState: "running"}
}
func testCall(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.AddCookie(&http.Cookie{Name: "panel_session", Value: "test-admin"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}
func TestDeletingUserPreservesOthersRules(t *testing.T) {
	a, mux, _ := testAPI(t)
	r := testRule("bob_rule", 3)
	a.store.UpsertRule(r)
	w := testCall(t, mux, "DELETE", "/api/users/2", nil)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	if got := a.store.GetRule("bob_rule").OwnerID; got != 3 {
		t.Fatalf("deleting alice stole bob_rule: owner=%d, want 3", got)
	}
}
func TestMigratingRuleRemovesOldLocalInstance(t *testing.T) {
	a, mux, log := testAPI(t)
	r := testRule("move_me", 1)
	a.store.UpsertRule(r)
	a.sd.WriteRuleFiles(a.store.ListRules(0, true), r)
	n, err := a.store.CreateNode("remote")
	if err != nil {
		t.Fatal(err)
	}
	next := *r
	next.NodeID = n.ID
	w := testCall(t, mux, "PUT", "/api/rules/move_me", next)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	calls, _ := os.ReadFile(log)
	_, err = os.Stat(filepath.Join(a.sd.confDir, "move_me.conf"))
	if err == nil || !strings.Contains(string(calls), "disable --now tcp-pool@move_me.service") {
		t.Fatalf("local-to-remote migration left local config=%v; systemctl calls=%q", err == nil, calls)
	}
}
func TestEditingStoppedRuleKeepsItStopped(t *testing.T) {
	a, mux, log := testAPI(t)
	r := testRule("stopped", 1)
	r.DesiredState = "stopped"
	a.store.UpsertRule(r)
	next := *r
	next.Remark = "updated remark"
	w := testCall(t, mux, "PUT", "/api/rules/stopped", next)
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "restart") {
		t.Fatalf("editing stopped rule restarted it while desired_state=%s; commands=%q", a.store.GetRule(r.Tag).DesiredState, calls)
	}
}
func TestLastAdminCannotDemoteSelf(t *testing.T) {
	a, mux, _ := testAPI(t)
	w := testCall(t, mux, "PUT", "/api/users/1", map[string]bool{"is_admin": false})
	if a.store.CountAdmins() == 0 {
		t.Fatalf("last admin demotion succeeded HTTP %d; remaining admins=0", w.Code)
	}
}
func TestTrafficPointsHaveTimestamps(t *testing.T) {
	a, _, _ := testAPI(t)
	tc := LoadTrafficCollector(a.store, filepath.Join(t.TempDir(), "traffic.json"), time.Minute)
	for _, r := range []string{"24h", "7d", "30d", "365d"} {
		pts := tc.Query(r, nil)["points"].([]trafficPoint)
		if pts[0].T == 0 {
			t.Errorf("range %s: first point timestamp=0 (Unix epoch)", r)
		}
	}
}
func TestSevenDaysIncludesFourDaysAgo(t *testing.T) {
	a, _, _ := testAPI(t)
	tc := LoadTrafficCollector(a.store, filepath.Join(t.TempDir(), "traffic.json"), time.Minute)
	now := time.Now()
	old := now.Add(-4 * 24 * time.Hour)
	tc.db.Buckets["rule"] = map[int64]*bucketVal{old.Unix(): {Up: 10}, now.Unix(): {Up: 20}}
	tc.db.Days["rule"] = map[string]*bucketVal{old.Format("2006-01-02"): {Up: 10}, now.Format("2006-01-02"): {Up: 20}}
	tc.pruneLocked(now)
	got := tc.Query("7d", nil)["totals"].(map[string]uint64)["up"]
	if got != 30 {
		t.Fatalf("7d total=%d, want30: four-day-old usage still in daily buckets but absent from query", got)
	}
}

func testNodeCall(t *testing.T, mux *http.ServeMux, key, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestNodeDeletionWaitsForCleanupAcknowledgement(t *testing.T) {
	a, mux, _ := testAPI(t)
	n, err := a.store.CreateNode("edge")
	if err != nil {
		t.Fatal(err)
	}
	r := testRule("edge_udp", 2)
	r.NodeID = n.ID
	r.EnableTCP = false
	r.EnableUDP = true
	r.RemoteUDPPort = 53
	if err := a.store.UpsertRule(r); err != nil {
		t.Fatal(err)
	}
	w := testCall(t, mux, "DELETE", "/api/nodes/"+fmt.Sprint(n.ID), nil)
	if w.Code != 200 || !a.store.GetNode(n.ID).Deleting {
		t.Fatalf("deletion: %d %s", w.Code, w.Body.String())
	}
	w = testNodeCall(t, mux, n.Key, "/api/node/pull", map[string]any{})
	var pull struct {
		Deleting bool              `json:"deleting"`
		Rules    []nodeRulePayload `json:"rules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pull); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !pull.Deleting || len(pull.Rules) != 0 {
		t.Fatalf("cleanup pull: %d %s", w.Code, w.Body.String())
	}
	for _, report := range []map[string]any{
		{"statuses": map[string]any{}},
		{"cleanup_complete": true, "statuses": map[string]any{"edge_udp": UnitStatus{Active: "active"}}},
		{"cleanup_complete": true, "statuses": map[string]any{}, "error": "stop failed"},
	} {
		testNodeCall(t, mux, n.Key, "/api/node/report", report)
		if a.store.GetNode(n.ID) == nil {
			t.Fatal("credential revoked before cleanup succeeded")
		}
	}
	r.Tag = "another"
	if err := a.store.CreateRule(r); err == nil {
		t.Fatal("deleting node accepted new rule")
	}
	w = testNodeCall(t, mux, n.Key, "/api/node/report", map[string]any{"cleanup_complete": true, "statuses": map[string]any{}})
	if w.Code != 200 || a.store.GetNode(n.ID) != nil {
		t.Fatalf("ack: %d %s", w.Code, w.Body.String())
	}
	if w = testNodeCall(t, mux, n.Key, "/api/node/pull", nil); w.Code != 401 {
		t.Fatalf("deleted credential accepted: %d", w.Code)
	}
}

func TestNodeCannotMarkOtherRulesOrStaleQuotaReset(t *testing.T) {
	a, mux, _ := testAPI(t)
	n, _ := a.store.CreateNode("edge")
	for i, tag := range []string{"local", "remote"} {
		r := testRule(tag, 1)
		r.ListenPort += i
		r.NodeID = int64(i) * n.ID
		if err := a.store.UpsertRule(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.SetRuleDesiredState("remote", "running"); err != nil {
		t.Fatal(err)
	}
	w := testNodeCall(t, mux, n.Key, "/api/node/report", map[string]any{"stats": map[string]*RuleStats{"local": {OverQuota: true}, "remote": {OverQuota: true}}})
	if w.Code != 200 || a.store.GetRule("local").OverQuota || a.store.GetRule("remote").OverQuota {
		t.Fatalf("unowned/stale report accepted: %s", w.Body.String())
	}
	reset := a.store.GetRule("remote").QuotaReset
	testNodeCall(t, mux, n.Key, "/api/node/report", map[string]any{"stats": map[string]*RuleStats{"remote": {OverQuota: true, QuotaReset: reset}}})
	if !a.store.GetRule("remote").OverQuota {
		t.Fatal("valid quota report discarded")
	}
}

func TestUDPOnlyAndSeparateProtocolsCanSharePort(t *testing.T) {
	a, mux, _ := testAPI(t)
	tcp := testRule("tcp", 1)
	if err := a.store.CreateRule(tcp); err != nil {
		t.Fatal(err)
	}
	udp := *tcp
	udp.Tag = "udp"
	udp.EnableTCP = false
	udp.EnableUDP = true
	udp.RemoteTCPPort = 0
	udp.RemoteUDPPort = 5353
	w := testCall(t, mux, "POST", "/api/rules", udp)
	if w.Code != 200 {
		t.Fatalf("UDP-only on TCP port: %d %s", w.Code, w.Body.String())
	}
	saved := a.store.GetRule("udp")
	if saved.EnableTCP || !saved.EnableUDP || saved.RemoteUDPPort != 5353 {
		t.Fatalf("protocol changed: %+v", saved)
	}
	udp.Tag = "collision"
	udp.ListenIP = "0.0.0.0"
	if w = testCall(t, mux, "POST", "/api/rules", udp); w.Code != 409 {
		t.Fatalf("wildcard UDP conflict accepted: %d", w.Code)
	}
}

func TestFailedUserDeleteAndDemotionHaveNoSideEffects(t *testing.T) {
	a, mux, _ := testAPI(t)
	r := testRule("bob", 3)
	a.store.UpsertRule(r)
	w := testCall(t, mux, "DELETE", "/api/users/9999", nil)
	if w.Code != 404 || a.store.GetRule("bob").OwnerID != 3 {
		t.Fatal("failed delete changed ownership")
	}
	w = testCall(t, mux, "PUT", "/api/users/1", map[string]any{"is_admin": false, "disabled": true, "password": "changed-pass"})
	u := a.store.GetUser(1)
	if w.Code != 400 || !u.IsAdmin || u.Disabled || u.PasswordHash != "" {
		t.Fatalf("rejected update mutated user: %+v", u)
	}
}

func TestImportPreservesNodesUnlessExplicitlyOverridden(t *testing.T) {
	a, mux, _ := testAPI(t)
	n, _ := a.store.CreateNode("edge")
	r := testRule("imported", 1)
	r.NodeID = n.ID
	w := testCall(t, mux, "POST", "/api/rules/import", map[string]any{"rules": []*Rule{r}})
	if w.Code != 200 || a.store.GetRule(r.Tag) == nil || a.store.GetRule(r.Tag).NodeID != n.ID {
		t.Fatalf("import lost node: %s", w.Body.String())
	}
	r.Tag = "local_import"
	r.ListenPort++
	w = testCall(t, mux, "POST", "/api/rules/import", map[string]any{"rules": []*Rule{r}, "node_id": 0})
	if w.Code != 200 || a.store.GetRule(r.Tag) == nil || a.store.GetRule(r.Tag).NodeID != 0 {
		t.Fatalf("explicit target ignored: %s", w.Body.String())
	}
}

func TestStoreSnapshotsDoNotMutateState(t *testing.T) {
	a, _, _ := testAPI(t)
	r := testRule("snapshot", 1)
	r.Advanced = map[string]int{"POOL_SIZE": 1}
	a.store.UpsertRule(r)
	u := a.store.GetUser(1)
	u.IsAdmin = false
	x := a.store.GetRule(r.Tag)
	x.NodeID = 123
	x.Advanced["POOL_SIZE"] = 99
	n, _ := a.store.CreateNode("edge")
	n.Key = "tampered"
	if !a.store.GetUser(1).IsAdmin || a.store.GetRule(r.Tag).NodeID != 0 || a.store.GetRule(r.Tag).Advanced["POOL_SIZE"] != 1 || a.store.GetNode(n.ID).Key == "tampered" {
		t.Fatal("store leaked mutable state")
	}
}

func TestTLSInstallationRecommendationDistinguishesProxy(t *testing.T) {
	a, mux, _ := testAPI(t)
	a.tls = &TLSConfig{Enabled: true, Generated: true, Fingerprint: "test-fingerprint"}
	for _, proxy := range []bool{false, true} {
		r := httptest.NewRequest("GET", "https://panel.test/api/tls", nil)
		r.TLS = &tls.ConnectionState{}
		r.AddCookie(&http.Cookie{Name: "panel_session", Value: "test-admin"})
		if proxy {
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var got struct {
			Pin bool `json:"pin_recommended"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || got.Pin == proxy {
			t.Fatalf("proxy=%v: %d %s", proxy, w.Code, w.Body.String())
		}
	}
}

func TestTrafficRowsAndTrendAgreeAcrossRanges(t *testing.T) {
	a, _, _ := testAPI(t)
	tc := LoadTrafficCollector(a.store, filepath.Join(t.TempDir(), "traffic.json"), time.Minute)
	zone := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 10, 24, 16, 17, 0, 0, zone)
	tc.db.Days["rule"] = map[string]*bucketVal{}
	for i := 0; i < 400; i++ {
		tc.db.Days["rule"][now.AddDate(0, 0, -i).Format("2006-01-02")] = &bucketVal{Up: 10, Down: 20, Conns: 1}
	}
	tc.db.Buckets["rule"] = map[int64]*bucketVal{now.Add(-23*time.Hour).Unix() / 300 * 300: {Up: 40, Down: 50, Conns: 2}}
	for _, key := range []string{"24h", "7d", "30d", "365d"} {
		got := tc.queryAt(key, nil, now)
		totals := got["totals"].(map[string]uint64)
		var up, down uint64
		var previous int64
		for _, p := range got["points"].([]trafficPoint) {
			if p.T <= previous {
				t.Fatalf("%s timestamps not ordered", key)
			}
			previous = p.T
			up += p.Up
			down += p.Down
		}
		if up != totals["up"] || down != totals["down"] {
			t.Fatalf("%s chart=%d/%d rows=%v", key, up, down, totals)
		}
		expected := map[string]uint64{"24h": 40, "7d": 70, "30d": 300, "365d": 3650}[key]
		if up != expected {
			t.Fatalf("%s up=%d want=%d", key, up, expected)
		}
	}
}

func TestLocalQuotaSurvivesEditUntilExplicitStart(t *testing.T) {
	a, mux, log := testAPI(t)
	old := statsDir
	statsDir = t.TempDir()
	t.Cleanup(func() { statsDir = old })
	r := testRule("quota", 1)
	r.TrafficQuotaBytes = 100
	if err := a.store.UpsertRule(r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statsPath(r.Tag), []byte(`{"up":200}`), 0600); err != nil {
		t.Fatal(err)
	}
	EnforceLocalQuotas(a.store, a.sd)
	if !a.store.GetRule(r.Tag).OverQuota {
		t.Fatal("quota flag missing")
	}
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	r.Remark = "edited while over quota"
	w := testCall(t, mux, "PUT", "/api/rules/quota", r)
	calls, _ := os.ReadFile(log)
	if w.Code != 200 || strings.Contains(string(calls), "restart") || !a.store.GetRule(r.Tag).OverQuota {
		t.Fatalf("edit resumed quota-blocked rule: %d %s %s", w.Code, w.Body.String(), calls)
	}
	w = testCall(t, mux, "POST", "/api/rules/quota/start", nil)
	if w.Code != 200 || a.store.GetRule(r.Tag).OverQuota || a.store.GetRule(r.Tag).QuotaReset == 0 {
		t.Fatalf("explicit start failed: %s", w.Body.String())
	}
	if _, err := os.Stat(statsPath(r.Tag)); !os.IsNotExist(err) {
		t.Fatal("old quota statistics were not cleared")
	}
}
