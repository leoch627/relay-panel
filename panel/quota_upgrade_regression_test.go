package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalQuotaStopSurvivesMissingOrCorruptStats(t *testing.T) {
	for _, contents := range []string{"", "not JSON"} {
		name := "missing"
		if contents != "" {
			name = "corrupt"
		}
		t.Run(name, func(t *testing.T) {
			a, _, log := testAPI(t)
			d := t.TempDir()
			old := statsDir
			statsDir = d
			t.Cleanup(func() { statsDir = old })
			r := testRule("blocked", 1)
			r.TrafficQuotaBytes = 100
			if err := a.store.UpsertRule(r); err != nil {
				t.Fatal(err)
			}
			a.store.SetRuleOverQuota(r.Tag, true)
			// Reload the persisted state as after a master reboot, without /run data.
			loaded, err := LoadStore(a.store.path)
			if err != nil {
				t.Fatal(err)
			}
			if contents != "" {
				if err := os.WriteFile(statsPath(r.Tag), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			EnforceLocalQuotas(loaded, a.sd)
			calls, _ := os.ReadFile(log)
			if !strings.Contains(string(calls), "disable --now tcp-pool@blocked.service") {
				t.Fatalf("persisted quota stop ignored with %s stats: %s", name, calls)
			}
			if !loaded.GetRule(r.Tag).OverQuota {
				t.Fatal("quota stop was cleared")
			}
		})
	}
}
func TestLocalMissingStatsDoesNotStopUnblockedRule(t *testing.T) {
	a, _, log := testAPI(t)
	old := statsDir
	statsDir = t.TempDir()
	t.Cleanup(func() { statsDir = old })
	r := testRule("allowed", 1)
	r.TrafficQuotaBytes = 100
	if err := a.store.UpsertRule(r); err != nil {
		t.Fatal(err)
	}
	EnforceLocalQuotas(a.store, a.sd)
	calls, _ := os.ReadFile(log)
	if len(calls) > 0 {
		t.Fatalf("missing stats stopped an unblocked rule: %s", calls)
	}
}
func TestSlaveKernelUpgradeRestartsOnlyAllowedRules(t *testing.T) {
	s, st, m := newSlaveTest(t)
	s.binPath = filepath.Join(t.TempDir(), "tcp_pool")
	if err := os.WriteFile(s.binPath, []byte("old kernel"), 0700); err != nil {
		t.Fatal(err)
	}
	if !s.refreshKernelState(st) {
		t.Fatal("initial kernel hash not recorded")
	}
	pr := slaveTestPull(1)
	pr.Rules = append(pr.Rules, nodeRulePayload{Tag: "stopped", Revision: 1, Desired: "stopped"}, nodeRulePayload{Tag: "blocked", Revision: 1, Desired: "running", Quota: 100})
	st.QuotaStops["blocked"] = quotaStop{Bytes: 200}
	s.reconcile(pr, st)
	starts := m.starts
	s.saveState(*st)
	loaded := s.loadState()
	if s.refreshKernelState(&loaded) {
		t.Fatal("unchanged binary invalidated revisions")
	}
	if err := os.WriteFile(s.binPath, []byte("new embedded kernel"), 0700); err != nil {
		t.Fatal(err)
	}
	if !s.refreshKernelState(&loaded) {
		t.Fatal("upgrade not detected")
	}
	s.saveState(loaded)
	// Pending restarts survive an agent restart between extraction and pull.
	loaded = s.loadState()
	s.enforceQuotas(pr, &loaded)
	s.reconcile(pr, &loaded)
	if m.starts != starts+1 || !m.running["demo"] || m.running["stopped"] || m.running["blocked"] {
		t.Fatalf("unexpected restarted rules: starts=%d before=%d running=%v", m.starts, starts, m.running)
	}
	if loaded.Applied["demo"] != 1 || loaded.Applied["stopped"] != 1 || loaded.Applied["blocked"] != 1 {
		t.Fatal("successful upgrade not recorded")
	}
}
