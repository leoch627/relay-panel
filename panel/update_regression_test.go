package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRelease 模拟 GitHub 的发布接口与下载地址。binary 是发行包内容，
// sum 为空时按 binary 计算，测试可以故意给错。
type fakeRelease struct {
	tag    string
	binary []byte
	sum    string
	hits   atomic.Int32
}

func fakeBinary(version string) []byte {
	return []byte("#!/bin/sh\necho \"relay-panel " + version + " (tcp_pool v1.8)\"\n")
}

func newTestUpdater(t *testing.T, rel *fakeRelease, slave bool) (*Updater, *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "relay-panel")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarts := &atomic.Int32{}
	u := &Updater{repo: "owner/repo", current: semver{1, 2, 0}, slave: slave, exe: exe,
		status: updateStatus{Phase: "idle"}, restart: func() error { restarts.Add(1); return nil }}
	asset, err := u.assetName()
	if err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel.hits.Add(1)
		base := "/owner/repo/releases/download/" + rel.tag + "/"
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": rel.tag, "body": "notes", "html_url": "https://evil.example/x"})
		case base + "SHA256SUMS":
			sum := rel.sum
			if sum == "" {
				h := sha256.Sum256(rel.binary)
				sum = hex.EncodeToString(h[:])
			}
			fmt.Fprintf(w, "%s  other-file\n%s  %s\n", strings.Repeat("0", 64), sum, asset)
		case base + asset:
			_, _ = w.Write(rel.binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u.apiBase, u.downloadBase, u.client = srv.URL, srv.URL, srv.Client()
	return u, restarts
}

func leftoverUpdateFiles(t *testing.T, u *Updater) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(u.exe), ".relay-panel-update-*"))
	return files
}

func TestSemverParsingAndOrdering(t *testing.T) {
	for _, bad := range []string{"", "v1.2", "1.2.3.4", "v1.2.3-rc1", "v1.2.3\n", "v1.2.x", "../v1.2.3", "v1.2.99999"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("parseSemver(%q) accepted", bad)
		}
	}
	a, ok := parseSemver("v1.10.0")
	b, ok2 := parseSemver("1.9.9")
	if !ok || !ok2 || !a.newerThan(b) || b.newerThan(a) || a.newerThan(a) {
		t.Fatalf("ordering wrong: %v %v", a, b)
	}
	if v, ok := versionFromBanner(panelVersion); !ok || v != currentSemver() {
		t.Fatalf("panelVersion %q does not carry panelSemver", panelVersion)
	}
	if _, ok := versionFromBanner("something v9.9.9"); ok {
		t.Fatal("foreign banner accepted")
	}
}

func TestUpdateFetchVerifiesThenInstallsWithBackup(t *testing.T) {
	rel := &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9")}
	u, _ := newTestUpdater(t, rel, false)
	tmp, err := u.Fetch(context.Background(), "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(tmp) != filepath.Dir(u.exe) {
		t.Fatalf("temp file %s is not next to the executable", tmp)
	}
	if err := u.Install(tmp); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(u.exe); string(got) != string(rel.binary) {
		t.Fatalf("executable not replaced: %q", got)
	}
	if got, _ := os.ReadFile(u.exe + ".bak"); string(got) != "old-binary" {
		t.Fatalf("backup missing or wrong: %q", got)
	}
	if left := leftoverUpdateFiles(t, u); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestUpdateRejectsTamperedOrMislabelledBinary(t *testing.T) {
	cases := map[string]*fakeRelease{
		"校验和不匹配":  {tag: "v9.9.9", binary: fakeBinary("v9.9.9"), sum: strings.Repeat("a", 64)},
		"不一致":     {tag: "v9.9.9", binary: fakeBinary("v9.9.8")},
		"无法在本机运行": {tag: "v9.9.9", binary: []byte("#!/bin/sh\nexit 3\n")},
	}
	for want, rel := range cases {
		u, _ := newTestUpdater(t, rel, false)
		tmp, err := u.Fetch(context.Background(), "v9.9.9")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got tmp=%q err=%v", want, tmp, err)
		}
		if got, _ := os.ReadFile(u.exe); string(got) != "old-binary" {
			t.Errorf("%s: executable was modified", want)
		}
		if left := leftoverUpdateFiles(t, u); len(left) != 0 {
			t.Errorf("%s: temp files left behind: %v", want, left)
		}
	}
}

func TestUpdateRefusesDowngradeAndBadTagsWithoutNetwork(t *testing.T) {
	rel := &fakeRelease{tag: "v1.0.0", binary: fakeBinary("v1.0.0")}
	u, _ := newTestUpdater(t, rel, true)
	for _, tag := range []string{"v1.0.0", "v1.2.0", "v9.9", "../../v9.9.9", "v9.9.9/../x"} {
		if _, err := u.Fetch(context.Background(), tag); err == nil {
			t.Errorf("Fetch(%q) succeeded", tag)
		}
	}
	if n := rel.hits.Load(); n != 0 {
		t.Fatalf("rejected versions still caused %d requests", n)
	}
}

func TestSelfUpdateOnlyAcceptsTheCheckedRelease(t *testing.T) {
	rel := &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9")}
	u, restarts := newTestUpdater(t, rel, false)
	if err := u.StartSelfUpdate("v9.9.9"); err == nil {
		t.Fatal("upgrade started before any update check")
	}
	latest, _, err := u.Latest(context.Background(), true)
	if err != nil || latest.Tag != "v9.9.9" {
		t.Fatalf("latest=%v err=%v", latest, err)
	}
	if !strings.HasPrefix(latest.URL, u.downloadBase+"/owner/repo/releases/tag/") {
		t.Fatalf("release link taken from the API response: %s", latest.URL)
	}
	if err := u.StartSelfUpdate("v9.9.8"); err == nil {
		t.Fatal("upgrade to a version other than the checked one accepted")
	}
	u.disabledReason = "disabled for test"
	if err := u.StartSelfUpdate("v9.9.9"); err == nil {
		t.Fatal("disabled updater started an upgrade")
	}
	u.disabledReason = ""
	if err := u.StartSelfUpdate("v9.9.9"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for restarts.Load() == 0 && u.Status().Phase != "failed" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Fatalf("restart not triggered: %+v", u.Status())
	}
	if got, _ := os.ReadFile(u.exe); string(got) != string(rel.binary) {
		t.Fatal("executable not replaced before restart")
	}
}

func TestUpdateEndpointsAreAdminOnly(t *testing.T) {
	a, mux, _ := testAPI(t)
	a.updater, _ = newTestUpdater(t, &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9")}, false)
	a.store.st.Sessions["alice"] = Session{UserID: 2, ExpiresAt: time.Now().Add(time.Hour)}
	for _, c := range [][2]string{{"GET", "/api/system/update"}, {"POST", "/api/system/update"}, {"POST", "/api/nodes/upgrade"}} {
		if w := testCallAs(t, mux, "alice", c[0], c[1], map[string]any{"version": "v9.9.9", "all": true}); w.Code != 403 {
			t.Errorf("%s %s as non-admin: HTTP %d", c[0], c[1], w.Code)
		}
	}
	w := testCall(t, mux, "GET", "/api/system/update", nil)
	var resp struct {
		Current   string       `json:"current"`
		Latest    *ReleaseInfo `json:"latest"`
		HasUpdate bool         `json:"has_update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	if resp.Current != "v1.2.0" || resp.Latest == nil || resp.Latest.Tag != "v9.9.9" || !resp.HasUpdate {
		t.Fatalf("unexpected status: %s", w.Body.String())
	}
}

func nodeCall(t *testing.T, mux *http.ServeMux, key, path string, body any) map[string]any {
	t.Helper()
	data, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", path, strings.NewReader(string(data)))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%s: HTTP %d: %s", path, w.Code, w.Body.String())
	}
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestNodeUpgradeIsQueuedDeliveredAndCleared(t *testing.T) {
	a, mux, _ := testAPI(t)
	a.updater, _ = newTestUpdater(t, &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9")}, false)
	modern, _ := a.store.CreateNode("modern")
	legacy, _ := a.store.CreateNode("legacy")
	silent, _ := a.store.CreateNode("never-reported")
	nodeCall(t, mux, modern.Key, "/api/node/report", map[string]any{"panel_version": "relay-panel v1.2.0 (tcp_pool v1.8)"})
	nodeCall(t, mux, legacy.Key, "/api/node/report", map[string]any{"panel_version": "relay-panel v1.1.0 (tcp_pool v1.8)"})

	w := testCall(t, mux, "POST", "/api/nodes/upgrade", map[string]any{"all": true})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"queued":1`) || !strings.Contains(w.Body.String(), `"skipped":2`) {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	if got := nodeCall(t, mux, modern.Key, "/api/node/pull", map[string]any{})["upgrade_to"]; got != "v9.9.9" {
		t.Fatalf("modern node not told to upgrade: %v", got)
	}
	for _, n := range []*Node{legacy, silent} {
		if got, ok := nodeCall(t, mux, n.Key, "/api/node/pull", map[string]any{})["upgrade_to"]; ok {
			t.Fatalf("node %s told to upgrade: %v", n.Name, got)
		}
	}

	// 升级失败的原因展示给管理员；仍低于目标版本时标记保留
	nodeCall(t, mux, modern.Key, "/api/node/report", map[string]any{"panel_version": "relay-panel v1.2.0 (tcp_pool v1.8)", "upgrade_error": "下载失败"})
	if w := testCall(t, mux, "GET", "/api/system/update", nil); !strings.Contains(w.Body.String(), "下载失败") {
		t.Fatalf("upgrade error not surfaced: %s", w.Body.String())
	}
	if a.store.GetNode(modern.ID).UpgradeTo != "v9.9.9" {
		t.Fatal("pending upgrade cleared before the node reached the target")
	}
	nodeCall(t, mux, modern.Key, "/api/node/report", map[string]any{"panel_version": "relay-panel v9.9.9 (tcp_pool v1.8)"})
	if got := a.store.GetNode(modern.ID).UpgradeTo; got != "" {
		t.Fatalf("pending upgrade not cleared after success: %q", got)
	}

	// 取消：重新下发后撤回
	nodeCall(t, mux, modern.Key, "/api/node/report", map[string]any{"panel_version": "relay-panel v1.2.0 (tcp_pool v1.8)"})
	testCall(t, mux, "POST", "/api/nodes/upgrade", map[string]any{"ids": []int64{modern.ID}})
	testCall(t, mux, "POST", "/api/nodes/upgrade", map[string]any{"ids": []int64{modern.ID}, "cancel": true})
	if got := a.store.GetNode(modern.ID).UpgradeTo; got != "" {
		t.Fatalf("cancel did not clear the pending upgrade: %q", got)
	}
}

func TestSlaveUpgradesOnlyToNewerValidReleases(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	rel := &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9")}
	var restarts *atomic.Int32
	s.updater, restarts = newTestUpdater(t, rel, true)

	for _, tag := range []string{"", "v1.2.0", "v1.0.0", "latest", "v9.9.9/../../x", "https://evil.example/x"} {
		s.checkUpgrade(tag)
	}
	if s.upgrade.running || s.upgrade.tried != "" || rel.hits.Load() != 0 {
		t.Fatalf("ignored targets started an upgrade: %+v hits=%d", s.upgrade, rel.hits.Load())
	}

	s.checkUpgrade("v9.9.9")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.upgradeMu.Lock()
		done := !s.upgrade.running
		s.upgradeMu.Unlock()
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if s.upgradeError() != "" || s.upgrade.ready == "" {
		t.Fatalf("download did not finish cleanly: %+v", s.upgrade)
	}
	if restarts.Load() != 0 {
		t.Fatal("restarted outside the main loop's safe point")
	}
	s.checkUpgrade("v9.9.9") // 下一轮：安装并重启
	if restarts.Load() != 1 {
		t.Fatalf("restart count = %d", restarts.Load())
	}
	if got, _ := os.ReadFile(s.updater.exe); string(got) != string(rel.binary) {
		t.Fatal("executable not replaced")
	}
}

func TestSlaveReportsFailedUpgradeAndBacksOff(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	rel := &fakeRelease{tag: "v9.9.9", binary: fakeBinary("v9.9.9"), sum: strings.Repeat("b", 64)}
	var restarts *atomic.Int32
	s.updater, restarts = newTestUpdater(t, rel, true)
	s.checkUpgrade("v9.9.9")
	deadline := time.Now().Add(10 * time.Second)
	for s.upgradeError() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(s.upgradeError(), "校验和不匹配") {
		t.Fatalf("failure not recorded: %q", s.upgradeError())
	}
	hits := rel.hits.Load()
	s.checkUpgrade("v9.9.9")
	time.Sleep(50 * time.Millisecond)
	if rel.hits.Load() != hits || restarts.Load() != 0 {
		t.Fatalf("retried immediately or restarted after a failed download (hits %d -> %d)", hits, rel.hits.Load())
	}
}
