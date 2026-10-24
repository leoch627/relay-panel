package main

import (
	"fmt"
	"sort"
	"strings"
)

// SystemdManager 负责把规则落到 /etc/tcp_pool/<tag>.conf 并管理 tcp-pool@<tag> 实例。
// 面板同时维护 relays.conf 镜像，保持原版 relay CLI 工具可用。
type SystemdManager struct {
	run        *Runner
	confDir    string
	relaysConf string
}

func NewSystemdManager(run *Runner, p Paths) *SystemdManager {
	return &SystemdManager{run: run, confDir: p.ConfDir, relaysConf: p.RelaysConf}
}

const serviceTemplate = `[Unit]
Description=High Performance TCP Connection Pool (C Version)
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/root/tcp_pool
EnvironmentFile=/etc/tcp_pool/%i.conf

Nice=-10
LimitNOFILE=1048576

Restart=always
RestartSec=3
User=root
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`

// EnvFileContent 生成 systemd EnvironmentFile 内容，键与底层 tcp_pool.c 一致。
func (m *SystemdManager) EnvFileContent(r *Rule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "LOCAL_IP=%s\n", r.ListenIP)
	fmt.Fprintf(&b, "LOCAL_PORT=%d\n", r.ListenPort)
	fmt.Fprintf(&b, "REMOTE_IP=%s\n", r.RemoteHost)
	fmt.Fprintf(&b, "REMOTE_TCP_PORT=%d\n", r.RemoteTCPPort)
	fmt.Fprintf(&b, "REMOTE_UDP_PORT=%d\n", r.RemoteUDPPort)
	fmt.Fprintf(&b, "ENABLE_TCP=%d\n", b2i(r.EnableTCP))
	fmt.Fprintf(&b, "ENABLE_UDP=%d\n", b2i(r.EnableUDP))
	//统计快照路径（新版内核支持；旧内核会忽略未知 env）
	fmt.Fprintf(&b, "STATS_FILE=%s\n", statsPath(r.Tag))
	if r.RateLimitKBps > 0 {
		fmt.Fprintf(&b, "RATE_LIMIT_KBPS=%d\n", r.RateLimitKBps)
	}
	for _, k := range advancedOrder {
		if v, ok := r.Advanced[k]; ok {
			fmt.Fprintf(&b, "%s=%d\n", k, v)
		}
	}
	return b.String()
}

// relaysMirror 生成与原版 tcp-pool-parse 兼容的 relays.conf 内容。
func relaysMirror(rules []*Rule) string {
	var b strings.Builder
	b.WriteString("# 由 relay-panel 自动维护；手动修改会被面板覆盖。请使用面板或 relay 命令管理。\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "\n[%s]\n", r.Tag)
		fmt.Fprintf(&b, "LOCAL_IP=%s\n", r.ListenIP)
		fmt.Fprintf(&b, "LOCAL_PORT=%d\n", r.ListenPort)
		fmt.Fprintf(&b, "REMOTE_IP=%s\n", r.RemoteHost)
		fmt.Fprintf(&b, "REMOTE_TCP_PORT=%d\n", r.RemoteTCPPort)
		fmt.Fprintf(&b, "REMOTE_UDP_PORT=%d\n", r.RemoteUDPPort)
		fmt.Fprintf(&b, "ENABLE_TCP=%d\n", b2i(r.EnableTCP))
		fmt.Fprintf(&b, "ENABLE_UDP=%d\n", b2i(r.EnableUDP))
		for _, k := range advancedOrder {
			if v, ok := r.Advanced[k]; ok {
				fmt.Fprintf(&b, "%s=%d\n", k, v)
			}
		}
	}
	return b.String()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// localRules 过滤出跑在本机的规则（relays.conf 镜像与本机 env 只反映本机规则）。
func localRules(rules []*Rule) []*Rule {
	out := make([]*Rule, 0, len(rules))
	for _, r := range rules {
		if r.NodeID == 0 {
			out = append(out, r)
		}
	}
	return out
}

// WriteRuleFiles 写入本机 <tag>.conf 并同步 relays.conf 镜像。
// 远端规则的 env 由被控机通过 pull 接口获取，不落在本机。
func (m *SystemdManager) WriteRuleFiles(rules []*Rule, changed *Rule) error {
	if changed.NodeID != 0 || m.run.DryRun {
		return nil
	}
	if err := writeFileAtomic(m.confDir+"/"+changed.Tag+".conf", []byte(m.EnvFileContent(changed)), 0o600); err != nil {
		return err
	}
	return writeFileAtomic(m.relaysConf, []byte(relaysMirror(localRules(rules))), 0o644)
}

// SyncAll 全量重写所有本机 env 文件与镜像（面板启动/规则删除后调用）。
func (m *SystemdManager) SyncAll(rules []*Rule) error {
	if m.run.DryRun {
		return nil
	}
	rules = localRules(rules)
	sort.Slice(rules, func(i, j int) bool { return rules[i].Tag < rules[j].Tag })
	for _, r := range rules {
		if err := writeFileAtomic(m.confDir+"/"+r.Tag+".conf", []byte(m.EnvFileContent(r)), 0o600); err != nil {
			return err
		}
	}
	return writeFileAtomic(m.relaysConf, []byte(relaysMirror(rules)), 0o644)
}

// RemoveRuleFiles 删除规则后清理本机 env 文件并刷新镜像。
func (m *SystemdManager) RemoveRuleFiles(rules []*Rule, tag string) error {
	if m.run.DryRun {
		return nil
	}
	_ = rmFile(m.confDir + "/" + tag + ".conf")
	return writeFileAtomic(m.relaysConf, []byte(relaysMirror(localRules(rules))), 0o644)
}

func (m *SystemdManager) unit(tag string) string { return "tcp-pool@" + tag + ".service" }

func (m *SystemdManager) StartRule(tag string) error {
	_, _, err := m.run.Run("systemctl", "enable", "--now", m.unit(tag))
	return err
}

func (m *SystemdManager) RestartRule(tag string) error {
	_, _, err := m.run.Run("systemctl", "enable", m.unit(tag))
	if err != nil {
		return err
	}
	_, _, err = m.run.Run("systemctl", "restart", m.unit(tag))
	return err
}

func (m *SystemdManager) StopRule(tag string) error {
	_, _, err := m.run.Run("systemctl", "disable", "--now", m.unit(tag))
	return err
}

func (m *SystemdManager) DeleteUnit(tag string) error {
	_, _, _ = m.run.Run("systemctl", "disable", "--now", m.unit(tag))
	_, _, _ = m.run.Run("systemctl", "reset-failed", m.unit(tag))
	return nil
}

type UnitStatus struct {
	Active    string `json:"active"`  // active / inactive / failed ...
	Enabled   string `json:"enabled"` // enabled / disabled ...
	StartedAt string `json:"started_at"`
	Restarts  string `json:"restarts"`
	MainPID   string `json:"main_pid"`
}

func (m *SystemdManager) Status(tag string) UnitStatus {
	st := UnitStatus{Active: "unknown", Enabled: "unknown"}
	out, _, _ := m.run.Run("systemctl", "is-active", m.unit(tag))
	st.Active = strings.TrimSpace(out)
	out, _, _ = m.run.Run("systemctl", "is-enabled", m.unit(tag))
	st.Enabled = strings.TrimSpace(out)
	out, _, _ = m.run.Run("systemctl", "show", m.unit(tag),
		"--property=ActiveEnterTimestamp,NRestarts,MainPID", "--no-pager")
	for _, line := range strings.Split(out, "\n") {
		kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "ActiveEnterTimestamp":
			st.StartedAt = kv[1]
		case "NRestarts":
			st.Restarts = kv[1]
		case "MainPID":
			st.MainPID = kv[1]
		}
	}
	return st
}

func (m *SystemdManager) Logs(tag string, lines int) (string, error) {
	n := fmt.Sprintf("%d", lines)
	out, _, err := m.run.Run("journalctl", "-u", m.unit(tag), "-n", n, "--no-pager", "-o", "short-iso")
	if m.run.DryRun {
		return "[dry-run] journalctl -u " + m.unit(tag) + "\n", nil
	}
	return out, err
}

func (m *SystemdManager) DaemonReload() {
	_, _, _ = m.run.Run("systemctl", "daemon-reload")
}
