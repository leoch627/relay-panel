package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// breakStorePath makes every later state save fail, independent of file permissions.
func breakStorePath(t *testing.T, s *Store) {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocker, "state.json")
}

// resetCommandLog clears recorded systemctl calls so a test only sees what follows.
func resetCommandLog(t *testing.T, log string) {
	t.Helper()
	if err := os.WriteFile(log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readCommandLog(t *testing.T, log string) string {
	t.Helper()
	data, _ := os.ReadFile(log)
	return string(data)
}

func TestSecurityHeadersOnResponses(t *testing.T) {
	_, mux, _ := testAPI(t)
	w := httptest.NewRecorder()
	securityHeaders(mux).ServeHTTP(w, httptest.NewRequest("GET", "/api/me", nil))
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing security headers: %v", w.Header())
	}
}

// The CSP forbids inline scripts, so the embedded page must only reference script files.
func TestEmbeddedIndexHasNoInlineScripts(t *testing.T) {
	html, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	for rest := page; ; {
		i := strings.Index(rest, "<script")
		if i < 0 {
			break
		}
		end := strings.Index(rest[i:], ">")
		if end < 0 {
			t.Fatal("unterminated script tag")
		}
		if tag := rest[i : i+end]; !strings.Contains(tag, "src=") {
			t.Fatalf("inline script would be blocked by CSP: %s", tag)
		}
		rest = rest[i+end:]
	}
	if _, err := fs.Stat(webFS, "web/theme-init.js"); err != nil || !strings.Contains(page, "/theme-init.js") {
		t.Fatalf("theme bootstrap script missing: %v", err)
	}
}

func TestBatchDeleteCountsRuleDeletedDespiteCleanupFailure(t *testing.T) {
	a, mux, _ := testAPI(t)
	r := testRule("half_clean", 1)
	if err := a.store.UpsertRule(r); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(blocker, nil, 0o600)
	a.sd.relaysConf = filepath.Join(blocker, "relays.conf")

	w := testCall(t, mux, "POST", "/api/rules/batch", map[string]any{"tags": []string{"half_clean"}, "action": "delete"})
	var res struct {
		Done, Skipped int
		Error         string
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != 200 || res.Done != 1 || res.Skipped != 0 || !strings.Contains(res.Error, "配置文件清理失败") {
		t.Fatalf("batch result: %d %s", w.Code, w.Body.String())
	}
	if a.store.GetRule("half_clean") != nil {
		t.Fatal("rule was not deleted")
	}
}

func TestBatchReportsFailureReason(t *testing.T) {
	a, mux, _ := testAPI(t)
	r := testRule("cannot_save", 1)
	if err := a.store.UpsertRule(r); err != nil {
		t.Fatal(err)
	}
	breakStorePath(t, a.store)
	w := testCall(t, mux, "POST", "/api/rules/batch", map[string]any{"tags": []string{"cannot_save", "missing"}, "action": "stop"})
	var res struct {
		Done, Skipped int
		Error         string
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Done != 0 || res.Skipped != 2 || !strings.HasPrefix(res.Error, "cannot_save：") {
		t.Fatalf("batch result: %s", w.Body.String())
	}
}

func TestFailedMigrationRestoresLocalInstance(t *testing.T) {
	a, mux, log := testAPI(t)
	r := testRule("stay_local", 1)
	a.store.UpsertRule(r)
	a.sd.WriteRuleFiles(a.store.ListRules(0, true), r)
	n, err := a.store.CreateNode("remote")
	if err != nil {
		t.Fatal(err)
	}
	resetCommandLog(t, log)
	breakStorePath(t, a.store)

	next := *r
	next.NodeID = n.ID
	if w := testCall(t, mux, "PUT", "/api/rules/stay_local", next); w.Code != 500 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	calls := readCommandLog(t, log)
	stop := strings.Index(calls, "disable --now tcp-pool@stay_local.service")
	restart := strings.LastIndex(calls, "restart tcp-pool@stay_local.service")
	if stop < 0 || restart < stop {
		t.Fatalf("local instance not restored after failed save; calls=%q", calls)
	}
	if got := a.store.GetRule("stay_local"); got == nil || got.NodeID != 0 {
		t.Fatalf("stored rule changed: %+v", got)
	}
}

func TestFailedDeleteRestoresLocalInstance(t *testing.T) {
	a, mux, log := testAPI(t)
	r := testRule("keep_running", 1)
	a.store.UpsertRule(r)
	a.sd.WriteRuleFiles(a.store.ListRules(0, true), r)
	resetCommandLog(t, log)
	breakStorePath(t, a.store)

	if w := testCall(t, mux, "DELETE", "/api/rules/keep_running", nil); w.Code != 500 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	calls := readCommandLog(t, log)
	if stop, restart := strings.Index(calls, "disable --now tcp-pool@keep_running.service"), strings.LastIndex(calls, "restart tcp-pool@keep_running.service"); stop < 0 || restart < stop {
		t.Fatalf("local instance not restored after failed delete; calls=%q", calls)
	}
	if a.store.GetRule("keep_running") == nil {
		t.Fatal("rule vanished")
	}
}

func TestLoginPrunesExpiredSessions(t *testing.T) {
	a, _, _ := testAPI(t)
	setTestPassword(t, a.store, 2, "alice-secret")
	a.store.st.Sessions["expired"] = Session{UserID: 2, ExpiresAt: time.Now().Add(-time.Minute)}
	a.store.st.Sessions["valid"] = Session{UserID: 3, ExpiresAt: time.Now().Add(time.Hour)}
	if _, _, err := a.store.Login("alice", "alice-secret", "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.store.st.Sessions["expired"]; ok {
		t.Fatal("expired session kept")
	}
	if _, ok := a.store.st.Sessions["valid"]; !ok {
		t.Fatal("valid session pruned")
	}
}

func TestSystemAndHostInfoAreAdminOnly(t *testing.T) {
	a, mux, _ := testAPI(t)
	a.tuner = NewTuner(a.run, DefaultPaths())
	a.store.st.Sessions["test-alice"] = Session{UserID: 2, ExpiresAt: time.Now().Add(time.Hour)}
	if w := testCallAs(t, mux, "test-alice", "GET", "/api/system", nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin /api/system: HTTP %d", w.Code)
	}
	overview := func(token string) map[string]any {
		t.Helper()
		w := testCallAs(t, mux, token, "GET", "/api/overview", nil)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatalf("overview as %s: %d %s", token, w.Code, w.Body.String())
		}
		return out
	}
	for _, key := range []string{"machine", "users_total", "tuning"} {
		if _, ok := overview("test-alice")[key]; ok {
			t.Errorf("non-admin overview exposes %s", key)
		}
		if _, ok := overview("test-admin")[key]; !ok {
			t.Errorf("admin overview lacks %s", key)
		}
	}
}
