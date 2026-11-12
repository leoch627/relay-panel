package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type slaveTestRunner struct {
	running  map[string]bool
	stopErr  error
	startErr error
	starts   int
	stops    int
}

func (m *slaveTestRunner) RestartRule(tag string) error {
	m.starts++
	if m.startErr != nil {
		return m.startErr
	}
	m.running[tag] = true
	return nil
}
func (m *slaveTestRunner) StopRule(tag string) error {
	m.stops++
	if m.stopErr != nil {
		return m.stopErr
	}
	delete(m.running, tag)
	return nil
}
func (m *slaveTestRunner) DeleteUnit(tag string) error { return m.StopRule(tag) }
func (m *slaveTestRunner) Status(tag string) UnitStatus {
	if m.running[tag] {
		return UnitStatus{Active: "active"}
	}
	return UnitStatus{Active: "inactive"}
}
func (m *slaveTestRunner) Logs(string, int) (string, error) { return "", nil }
func newSlaveTest(t *testing.T) (*Slave, *slaveState, *slaveTestRunner) {
	t.Helper()
	d := t.TempDir()
	original := statsDir
	statsDir = d
	t.Cleanup(func() { statsDir = original })
	s := NewSlave("https://master.example", "key", &Runner{}, Paths{ConfDir: d, TCPPoolBin: "/unused"}, nil, false)
	s.statePath = filepath.Join(d, "slave-state.json")
	m := &slaveTestRunner{running: map[string]bool{}}
	s.mgr = m
	st := s.loadState()
	return s, &st, m
}
func writeSlaveStats(t *testing.T, tag string, up uint64) {
	t.Helper()
	b, _ := json.Marshal(&RuleStats{Up: up})
	if err := os.WriteFile(statsPath(tag), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func slaveTestPull(rev int64) *pullResponse {
	return &pullResponse{Rules: []nodeRulePayload{{Tag: "demo", Revision: rev, Desired: "running", Env: "REMOTE_IP=127.0.0.1\nREMOTE_TCP_PORT=443\nENABLE_TCP=1\n", Quota: 100}}}
}

func TestSlaveQuotaSurvivesRevisionAndAgentRestart(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	writeSlaveStats(t, "demo", 200)
	stats, changed := s.enforceQuotas(pr, st)
	if !changed || !stats["demo"].OverQuota || m.running["demo"] {
		t.Fatal("quota failed to stop")
	}
	s.saveState(*st)
	loaded := s.loadState()
	if len(loaded.Rules) != 0 {
		t.Fatal("unexpected rules")
	}
	if err := os.Remove(statsPath("demo")); err != nil {
		t.Fatal(err)
	}
	pr.Rules[0].Revision = 2
	s.syncQuotaState(pr, &loaded)
	s.enforceQuotas(pr, &loaded)
	s.reconcile(pr, &loaded)
	if m.running["demo"] || len(loaded.QuotaStops) != 1 {
		t.Fatal("revision or restart released quota stop")
	}
	pr.Rules[0].Quota = 150
	s.syncQuotaState(pr, &loaded)
	if len(loaded.QuotaStops) != 1 {
		t.Fatal("insufficient quota increase released stop")
	}
	pr.Rules[0].Quota = 201
	pr.Rules[0].Revision = 3
	s.syncQuotaState(pr, &loaded)
	s.reconcile(pr, &loaded)
	if !m.running["demo"] {
		t.Fatal("sufficient increase did not restore rule")
	}
}
func TestSlaveExplicitResetRemovesOldStats(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	writeSlaveStats(t, "demo", 200)
	s.enforceQuotas(pr, st)
	pr.Rules[0].Revision = 2
	pr.Rules[0].QuotaReset = 123
	s.syncQuotaState(pr, st)
	s.enforceQuotas(pr, st)
	s.reconcile(pr, st)
	if !m.running["demo"] || len(st.QuotaStops) != 0 {
		t.Fatal("explicit reset failed")
	}
	if _, err := os.Stat(statsPath("demo")); !os.IsNotExist(err) {
		t.Fatal("stale stats retained")
	}
	writeSlaveStats(t, "demo", 101)
	stats, _ := s.enforceQuotas(pr, st)
	if stats["demo"].QuotaReset != 123 || m.running["demo"] {
		t.Fatal("fresh usage not enforced/reported with reset token")
	}
}
func TestSlaveFailedResetCannotRestart(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	before := m.starts
	pr.Rules[0].Revision = 2
	pr.Rules[0].QuotaReset = 1
	m.stopErr = errors.New("cannot stop")
	s.syncQuotaState(pr, st)
	s.enforceQuotas(pr, st)
	s.reconcile(pr, st)
	if m.starts != before || st.Applied["demo"] != 1 || st.QuotaResets["demo"] != 0 {
		t.Fatal("failed reset was applied")
	}
}
func TestSlaveNewRuleAcceptsExistingResetToken(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	pr.Rules[0].QuotaReset = 30
	m.stopErr = errors.New("missing unit")
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	if !m.running["demo"] || m.stops != 0 {
		t.Fatal("new rule unnecessarily stopped")
	}
}
func TestSlaveDeletionRetriesAndReportsPendingInstance(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	s.reconcile(pr, st)
	pr.Rules = nil
	pr.Deleting = true
	m.stopErr = errors.New("failure")
	s.reconcile(pr, st)
	if len(st.Applied) != 1 || len(s.collectStatuses(pr, st)) != 1 {
		t.Fatal("failed deletion disappeared from report")
	}
	m.stopErr = nil
	s.reconcile(pr, st)
	if len(st.Applied) != 0 || len(s.collectStatuses(pr, st)) != 0 {
		t.Fatal("successful deletion still pending")
	}
}
func TestSlaveFailedStartRemainsPendingForCleanup(t *testing.T) {
	s, st, m := newSlaveTest(t)
	m.startErr = errors.New("failed restart")
	pr := slaveTestPull(9)
	s.reconcile(pr, st)
	revision, ok := st.Applied["demo"]
	if !ok || revision == 9 {
		t.Fatal("failed start not tracked as pending")
	}
	pr.Rules = nil
	m.stopErr = errors.New("failed cleanup")
	s.reconcile(pr, st)
	if len(s.collectStatuses(pr, st)) != 1 {
		t.Fatal("pending instance forgotten")
	}
}
func TestSlaveOfflineQuotaUsesPersistedRules(t *testing.T) {
	s, st, m := newSlaveTest(t)
	pr := slaveTestPull(1)
	s.reconcile(pr, st)
	st.Rules = pr.Rules
	s.saveState(*st)
	loaded := s.loadState()
	writeSlaveStats(t, "demo", 200)
	s.enforceQuotas(&pullResponse{Rules: loaded.Rules}, &loaded)
	if m.running["demo"] || len(loaded.QuotaStops) != 1 {
		t.Fatal("offline cached quota not enforced")
	}
}
func TestSlaveProbeDoesNotBlockAndLimitsConcurrency(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	var active, peak, calls atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{}, 12)
	s.probe = func(string, int, bool) *ProbeResult {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		calls.Add(1)
		entered <- struct{}{}
		<-release
		active.Add(-1)
		return &ProbeResult{Ok: true}
	}
	pr := &pullResponse{}
	for i := 0; i < 12; i++ {
		pr.Rules = append(pr.Rules, nodeRulePayload{Tag: fmt.Sprint(i), Env: "REMOTE_IP=example.com\nREMOTE_TCP_PORT=443\nENABLE_TCP=1\n"})
	}
	start := time.Now()
	s.probeRemotes(pr)
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("probe blocked polling")
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("workers not running")
		}
	}
	if peak.Load() > 4 {
		t.Fatal("probe exceeded worker limit")
	}
	s.probeRemotes(pr)
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.probeMu.Lock()
		running := s.probeRunning
		s.probeMu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	got := s.probeRemotes(pr)
	if len(got) != 12 || calls.Load() != 12 {
		t.Fatalf("cached results=%d calls=%d", len(got), calls.Load())
	}
}
func TestSlaveUDPProbeHonorsProtocol(t *testing.T) {
	host, port, soft, ok := nodeProbeTarget(nodeRulePayload{Env: "REMOTE_IP=example.com\nREMOTE_TCP_PORT=443\nREMOTE_UDP_PORT=5353\nENABLE_TCP=0\nENABLE_UDP=1\n"})
	if !ok || host != "example.com" || port != 5353 || !soft {
		t.Fatal("UDP-only rule probed TCP port as hard failure")
	}
}
func TestSlaveLauncherPreservesArguments(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	d := t.TempDir()
	bin := filepath.Join(d, "panel with 'quote'")
	argFile := filepath.Join(d, "args")
	t.Setenv("SLAVE_TEST_ARGS", argFile)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SLAVE_TEST_ARGS\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.masterURL = "https://master.example/a?x=$oops&z='yes'"
	s.tlsFingerprint = "abc123"
	s.tlsInsecure = true
	s.direct = true
	s.confDir = filepath.Join(d, "config space")
	launch := filepath.Join(d, "launcher")
	if err := os.WriteFile(launch, []byte(s.launchScript(bin)), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(launch).CombinedOutput(); err != nil {
		t.Fatalf("launcher: %v %s", err, out)
	}
	b, _ := os.ReadFile(argFile)
	want := []string{"-slave", "-master", s.masterURL, "-node-key", s.nodeKey, "-conf-dir", s.confDir, "-tcpool-bin", s.binPath, "-state", filepath.Join(filepath.Dir(s.statePath), "panel.json"), "-direct", "-tls-fingerprint", "abc123", "-tls-insecure"}
	if !reflect.DeepEqual(strings.Split(strings.TrimSuffix(string(b), "\n"), "\n"), want) {
		t.Fatalf("arguments changed: %s", b)
	}
}
func TestSlaveOpenRCInstallationStartsService(t *testing.T) {
	s, _, _ := newSlaveTest(t)
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	t.Setenv("SLAVE_TEST_COMMANDS", log)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, name := range []string{"rc-service", "rc-update"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$SLAVE_TEST_COMMANDS\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.tlsFingerprint = "fixed-pin"
	if err := s.installService("/custom/panel", false, root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "add relay-panel-slave default") || !strings.Contains(string(b), "relay-panel-slave restart") {
		t.Fatalf("did not enable/start: %s", b)
	}
	launch, _ := os.ReadFile(filepath.Join(filepath.Dir(s.statePath), "slave-launch"))
	if !strings.Contains(string(launch), "fixed-pin") {
		t.Fatal("pin missing")
	}
	if _, err := os.Stat(filepath.Join(root, "etc/init.d/relay-panel-slave")); err != nil {
		t.Fatal(err)
	}
}
func TestProcessStopAndQuotaAreRaceFree(t *testing.T) {
	s, st, _ := newSlaveTest(t)
	d := t.TempDir()
	t.Setenv("PANEL_LOG_DIR", d)
	bin := filepath.Join(d, "fake-tcp-pool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	s.proc = NewProcessManager(s.run, Paths{ConfDir: s.confDir, TCPPoolBin: bin})
	s.mgr = s.proc
	defer s.proc.StopRule("demo")
	pr := slaveTestPull(1)
	s.reconcile(pr, st)
	writeSlaveStats(t, "demo", 200)
	s.enforceQuotas(pr, st)
	s.ensureRunning(pr, st)
	s.enforceQuotas(pr, st)
	if s.proc.Alive("demo") {
		t.Fatal("quota stop was undone by self-healing")
	}
	pr.Rules[0].QuotaReset = 1
	pr.Rules[0].Revision = 2
	s.syncQuotaState(pr, st)
	s.reconcile(pr, st)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.proc.Status("demo")
			s.proc.Alive("demo")
		}
	}()
	if err := s.proc.StopRule("demo"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if s.proc.Alive("demo") {
		t.Fatal("process still alive")
	}
}
