package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Slave 被控模式：轮询主控拉取期望规则集，本地管理 tcp_pool 实例并回传状态。
// 有 systemd 时用 systemd 单元；没有（Alpine/OpenRC 等）时直跑子进程。
// 全程出站连接，被控机可在 NAT 后面，无需开放任何入站端口。
type Slave struct {
	masterURL     string
	nodeKey       string
	run           *Runner
	mgr           RuleRunner      // systemd 或直跑
	proc          *ProcessManager // 直跑模式非 nil
	direct        bool
	confDir       string
	statePath     string
	binPath       string
	client        *http.Client
	overQuotaSent map[string]bool //本周期已因超限停机的规则，避免重复stop
}

type slaveState struct {
	Applied map[string]int64 `json:"applied"` // tag -> 已应用版本（revision）
}

func NewSlave(masterURL, nodeKey string, run *Runner, p Paths, tlsCfg *tls.Config, direct bool) *Slave {
	transport := &http.Transport{}
	if tlsCfg != nil {
		transport.TLSClientConfig = tlsCfg
	}
	s := &Slave{
		masterURL: masterURL,
		nodeKey:   nodeKey,
		run:       run,
		direct:    direct,
		confDir:   p.ConfDir,
		statePath: "/etc/relay-panel/slave-state.json",
		binPath:   p.TCPPoolBin,
		client:    &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}
	if direct {
		s.proc = NewProcessManager(run, p)
		s.mgr = s.proc
	} else {
		s.mgr = NewSystemdManager(run, p)
	}
	return s
}

// EnsureEmbeddedTCPPool 单文件客户端自解压：内嵌 musl 静态 tcp_pool 覆盖安装
// （缺失、无法运行或版本缺少新特性时）。非内嵌构建为空操作。
func (s *Slave) EnsureEmbeddedTCPPool() error {
	if !tcpoolEmbedded() || s.run.DryRun {
		return nil
	}
	needWrite := true
	if out, _, err := s.run.Run(s.binPath, "--version"); err == nil {
		if strings.Contains(out, "enable_tcp") {
			needWrite = false //已有可用版本
		}
	}
	if !needWrite {
		return nil
	}
	log.Printf("[slave] 自解压内嵌 tcp_pool -> %s", s.binPath)
	if err := writeFileAtomic(s.binPath, embeddedTCPPool, 0o755); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", s.binPath, err)
	}
	if out, _, err := s.run.Run(s.binPath, "--version"); err != nil || !strings.Contains(out, "enable_tcp") {
		return fmt.Errorf("内嵌 tcp_pool 无法在本机运行（%v）", err)
	}
	return nil
}

type pullResponse struct {
	NodeID   int64             `json:"node_id"`
	Interval int               `json:"interval"`
	Rules    []nodeRulePayload `json:"rules"`
	Error    string            `json:"error"`
}

func (s *Slave) request(method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, s.masterURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.nodeKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == 401 {
		return fmt.Errorf("节点密钥无效（401）：请检查主控侧是否重置过密钥")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("主控返回 %d: %s", resp.StatusCode, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *Slave) loadState() slaveState {
	st := slaveState{Applied: map[string]int64{}}
	data, err := os.ReadFile(s.statePath)
	if err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Applied == nil {
		st.Applied = map[string]int64{}
	}
	return st
}

func (s *Slave) saveState(st slaveState) {
	if s.run.DryRun {
		return
	}
	data, _ := json.MarshalIndent(&st, "", "  ")
	_ = writeFileAtomic(s.statePath, data, 0o600)
}

// reconcile 把本地实例收敛到期望状态，返回是否有变更。
func (s *Slave) reconcile(pr *pullResponse, st *slaveState) bool {
	changed := false
	desired := map[string]nodeRulePayload{}
	for _, r := range pr.Rules {
		desired[r.Tag] = r
	}

	// 1) 清理不再属于本节点的实例
	for tag := range st.Applied {
		if _, ok := desired[tag]; ok {
			continue
		}
		log.Printf("[slave] 移除实例 %s", tag)
		_ = s.mgr.DeleteUnit(tag)
		_ = rmFile(s.confDir + "/" + tag + ".conf")
		delete(st.Applied, tag)
		changed = true
	}

	// 2) 应用新增/变更的规则
	for tag, r := range desired {
		if st.Applied[tag] == r.Revision {
			if _, err := os.Stat(s.confDir + "/" + tag + ".conf"); err == nil || s.run.DryRun {
				continue // 版本一致且配置文件在，无需动作
			}
		}
		log.Printf("[slave] 应用实例 %s (rev=%d desired=%s)", tag, r.Revision, r.Desired)
		if s.run.DryRun {
			log.Printf("[dry-run] write %s/%s.conf (%d bytes)", s.confDir, tag, len(r.Env))
			if r.Desired == "stopped" {
				_ = s.mgr.StopRule(tag)
			} else {
				_ = s.mgr.RestartRule(tag)
			}
			st.Applied[tag] = r.Revision
			changed = true
			continue
		}
		if err := writeFileAtomic(s.confDir+"/"+tag+".conf", []byte(r.Env), 0o600); err != nil {
			log.Printf("[slave] 写入 %s.conf 失败: %v", tag, err)
			continue
		}
		if r.Desired == "stopped" {
			_ = s.mgr.StopRule(tag)
		} else {
			if err := s.mgr.RestartRule(tag); err != nil {
				log.Printf("[slave] 启动 %s 失败: %v", tag, err)
			}
		}
		st.Applied[tag] = r.Revision
		changed = true
	}
	return changed
}

// collectStatuses 汇总期望规则的本机 systemd 状态用于上报。
func (s *Slave) collectStatuses(pr *pullResponse) map[string]UnitStatus {
	statuses := map[string]UnitStatus{}
	for _, r := range pr.Rules {
		statuses[r.Tag] = s.mgr.Status(r.Tag)
	}
	return statuses
}

func (s *Slave) tcpPoolVersion() string {
	out, _, err := s.run.Run(s.binPath, "--version")
	if err != nil {
		return "unavailable"
	}
	v := out
	if v == "" { // dry-run
		v = "(dry-run)"
	}
	return v
}

// enforceQuotas 节点本地配额执行：读取本机 stats 文件，超限即停机并标记。
func (s *Slave) enforceQuotas(pr *pullResponse) map[string]*RuleStats {
	stats := map[string]*RuleStats{}
	for _, r := range pr.Rules {
		st := ReadRuleStats(r.Tag)
		if st == nil {
			continue
		}
		if r.Quota > 0 && st.Total() >= uint64(r.Quota) {
			if !s.overQuotaSent[r.Tag] {
				log.Printf("[slave] 规则 %s 已超配额（%d/%d 字节），执行停机", r.Tag, st.Total(), r.Quota)
				_ = s.mgr.StopRule(r.Tag)
				s.overQuotaSent[r.Tag] = true
			}
			st.OverQuota = true
		} else if r.Quota <= 0 {
			delete(s.overQuotaSent, r.Tag) //配额被取消后允许重新计数
		}
		stats[r.Tag] = st
	}
	return stats
}

// probeRemotes 节点侧定期测活：对本节点规则 TCP 建连远端测时延。
func (s *Slave) probeRemotes(pr *pullResponse) map[string]*ProbeResult {
	probes := map[string]*ProbeResult{}
	for _, r := range pr.Rules {
		parts := strings.SplitN(r.Env, "\n", -1)
		host, tcpPort, udpPort := "", 0, 0
		for _, line := range parts {
			kv := strings.SplitN(line, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "REMOTE_IP":
				host = kv[1]
			case "REMOTE_TCP_PORT":
				fmt.Sscanf(kv[1], "%d", &tcpPort)
			case "REMOTE_UDP_PORT":
				fmt.Sscanf(kv[1], "%d", &udpPort)
			}
		}
		port, soft := 0, false
		if tcpPort > 0 {
			port = tcpPort
		} else if udpPort > 0 {
			port, soft = udpPort, true
		} else {
			continue
		}
		probes[r.Tag] = probeOne(host, port, soft)
	}
	return probes
}

func (s *Slave) Run() {
	if err := s.EnsureEmbeddedTCPPool(); err != nil {
		log.Printf("[slave] %v", err)
	}
	st := s.loadState()
	tcpVer := s.tcpPoolVersion()
	s.overQuotaSent = map[string]bool{}
	interval := 5 * time.Second
	mode := "systemd"
	if s.direct {
		mode = "direct"
	}
	log.Printf("[slave] 启动：master=%s 实例管理=%s 状态文件=%s", s.masterURL, mode, s.statePath)

	for {
		var pr pullResponse
		err := s.request("POST", "/api/node/pull", map[string]string{}, &pr)
		if err != nil {
			log.Printf("[slave] 拉取失败: %v", err)
			// 拉取失败保持现状不动（避免主控抖动引发误清理）
		} else {
			if pr.Interval >= 1 && pr.Interval <= 60 {
				interval = time.Duration(pr.Interval) * time.Second
			}
			if s.reconcile(&pr, &st) {
				s.saveState(st)
			}
			// 直跑模式：崩溃/被杀的实例自愈
			if s.proc != nil {
				for _, r := range pr.Rules {
					if r.Desired == "running" {
						s.proc.EnsureAlive(r.Tag)
					}
				}
			}
			// 上报状态、流量统计与节点侧测活结果
			report := map[string]any{
				"machine":          ReadMachineInfo(),
				"tcp_pool_version": tcpVer,
				"panel_version":    panelVersion,
				"statuses":         s.collectStatuses(&pr),
				"stats":            s.enforceQuotas(&pr),
				"probes":           s.probeRemotes(&pr),
				"error":            "",
			}
			if rerr := s.request("POST", "/api/node/report", report, nil); rerr != nil {
				log.Printf("[slave] 上报失败: %v", rerr)
			}
		}
		time.Sleep(interval)
	}
}

// SelfInstall 单文件客户端自安装：解压 tcp_pool + 注册自启动服务（systemd 或 OpenRC）后退出。
func (s *Slave) SelfInstall(binPath string) error {
	if err := s.EnsureEmbeddedTCPPool(); err != nil {
		return err
	}
	if !hasSystemd() {
		// OpenRC（Alpine 等）
		_ = os.MkdirAll("/etc/init.d", 0o755)
		script := `#!/sbin/openrc-run
description="Relay Panel Slave Node Agent"
command="` + binPath + `"
command_args="-slave -master ` + s.masterURL + ` -node-key ` + s.nodeKey + `"
command_background=true
pidfile="/run/relay-panel-slave.pid"
output_log="/var/log/relay-panel/slave.log"
error_log="/var/log/relay-panel/slave.log"

depend() {
	need net
	after firewall
}
`
		if err := writeFileAtomic("/etc/init.d/relay-panel-slave", []byte(script), 0o755); err != nil {
			return err
		}
		log.Printf("[slave] 已写入 OpenRC 服务 /etc/init.d/relay-panel-slave")
		log.Printf("[slave] 启动：rc-service relay-panel-slave start && rc-update add relay-panel-slave default")
		return nil
	}
	_ = writeFileAtomic("/etc/systemd/system/tcp-pool@.service", []byte(serviceTemplate), 0o644)
	unit := `[Unit]
Description=Relay Panel Slave Node Agent
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=` + binPath + ` -slave -master ` + s.masterURL + ` -node-key ` + s.nodeKey + `
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`
	if err := writeFileAtomic("/etc/systemd/system/relay-panel-slave.service", []byte(unit), 0o644); err != nil {
		return err
	}
	_, _, _ = s.run.Run("systemctl", "daemon-reload")
	if _, _, err := s.run.Run("systemctl", "enable", "relay-panel-slave"); err != nil {
		return err
	}
	if _, _, err := s.run.Run("systemctl", "restart", "relay-panel-slave"); err != nil {
		return err
	}
	log.Printf("[slave] 已安装并启动 systemd 服务 relay-panel-slave")
	return nil
}
