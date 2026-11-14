package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Slave 被控模式：轮询主控拉取期望规则集，本地管理 tcp_pool 实例并回传状态。
// 有 systemd 时用 systemd 单元；没有（Alpine/OpenRC 等）时直跑子进程。
// 全程出站连接，被控机可在 NAT 后面，无需开放任何入站端口。
type Slave struct {
	masterURL      string
	nodeKey        string
	run            *Runner
	mgr            RuleRunner      // systemd 或直跑
	proc           *ProcessManager // 直跑模式非 nil
	direct         bool
	confDir        string
	statePath      string
	binPath        string
	client         *http.Client
	tlsFingerprint string
	tlsInsecure    bool
	probeMu        sync.Mutex
	probeResults   map[string]*ProbeResult
	probeLast      time.Time
	probeRunning   bool
	probe          func(string, int, bool) *ProbeResult
	updater        *Updater
	upgradeMu      sync.Mutex
	upgrade        slaveUpgrade
}

// slaveUpgrade 记录在线升级的进度。下载在后台进行，不阻塞配置拉取与配额巡检；
// 替换与重启只在主循环的安全点执行。
type slaveUpgrade struct {
	running bool
	tried   string // 最近一次尝试的目标版本
	triedAt time.Time
	ready   string // 已下载并校验、等待安装的临时文件
	err     string // 最近一次失败原因，随上报回传主控
}

type slaveState struct {
	Applied     map[string]int64     `json:"applied"` // tag -> 已应用版本（revision）
	QuotaStops  map[string]quotaStop `json:"quota_stops,omitempty"`
	QuotaResets map[string]int64     `json:"quota_resets,omitempty"`
	Rules       []nodeRulePayload    `json:"rules,omitempty"` // last pull, also used for offline quota checks
	KernelHash  string               `json:"kernel_hash,omitempty"`
}

type quotaStop struct {
	Bytes uint64 `json:"bytes"`
	Reset int64  `json:"reset"`
}

func NewSlave(masterURL, nodeKey string, run *Runner, p Paths, tlsCfg *tls.Config, direct bool) *Slave {
	transport := &http.Transport{}
	if tlsCfg != nil {
		transport.TLSClientConfig = tlsCfg
	}
	s := &Slave{
		masterURL:    masterURL,
		nodeKey:      nodeKey,
		run:          run,
		direct:       direct,
		confDir:      p.ConfDir,
		statePath:    "/etc/relay-panel/slave-state.json",
		binPath:      p.TCPPoolBin,
		client:       &http.Client{Timeout: 15 * time.Second, Transport: transport},
		probeResults: map[string]*ProbeResult{},
		probe:        probeOne,
		updater:      NewUpdater(run.DryRun, true),
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
		if strings.Contains(out, "udp_rate_limit") {
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
	if out, _, err := s.run.Run(s.binPath, "--version"); err != nil || !strings.Contains(out, "udp_rate_limit") {
		return fmt.Errorf("内嵌 tcp_pool 无法在本机运行（%v）", err)
	}
	return nil
}

type pullResponse struct {
	NodeID    int64             `json:"node_id"`
	Interval  int               `json:"interval"`
	Rules     []nodeRulePayload `json:"rules"`
	Error     string            `json:"error"`
	Deleting  bool              `json:"deleting"`
	UpgradeTo string            `json:"upgrade_to"` // 主控要求升级到的版本标签
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
	if st.QuotaStops == nil {
		st.QuotaStops = map[string]quotaStop{}
	}
	if st.QuotaResets == nil {
		st.QuotaResets = map[string]int64{}
	}
	// 旧版本可能把未经校验的标签写进了状态文件，加载时一并丢弃
	for tag := range st.Applied {
		if !tagRe.MatchString(tag) {
			delete(st.Applied, tag)
		}
	}
	for tag := range st.QuotaStops {
		if !tagRe.MatchString(tag) {
			delete(st.QuotaStops, tag)
		}
	}
	for tag := range st.QuotaResets {
		if !tagRe.MatchString(tag) {
			delete(st.QuotaResets, tag)
		}
	}
	st.Rules = validPulledRules(st.Rules)
	return st
}

// validPulledRules 丢弃标签不合法的下发规则。标签会拼进配置、统计文件路径和
// systemd 实例名，而被控端以 root 运行；主控虽已校验，这里再校验一次，防止主控
// 被攻破或被冒充（例如 -tls-insecure）时借 "../" 写到任意路径。
func validPulledRules(rules []nodeRulePayload) []nodeRulePayload {
	out := make([]nodeRulePayload, 0, len(rules))
	for _, r := range rules {
		if !tagRe.MatchString(r.Tag) {
			log.Printf("[slave] 忽略标签不合法的规则 %q", r.Tag)
			continue
		}
		out = append(out, r)
	}
	return out
}

func (s *Slave) saveState(st slaveState) {
	if s.run.DryRun {
		return
	}
	data, _ := json.MarshalIndent(&st, "", "  ")
	_ = writeFileAtomic(s.statePath, data, 0o600)
}

// Replacing an executable does not replace already running systemd processes.
// Invalidate applied revisions once per binary change, then let reconcile use
// the latest desired state and quota stops before restarting any instance.
func (s *Slave) refreshKernelState(st *slaveState) bool {
	if s.run.DryRun {
		return false
	}
	data, err := os.ReadFile(s.binPath)
	if err != nil {
		return false
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if hash == st.KernelHash {
		return false
	}
	for tag := range st.Applied {
		st.Applied[tag] = -1
	}
	st.KernelHash = hash
	return true
}

// syncQuotaState only releases a quota stop after an explicit reset or a sufficient
// quota increase. Configuration revisions alone must never restore a blocked rule.
func (s *Slave) syncQuotaState(pr *pullResponse, st *slaveState) bool {
	if st.QuotaStops == nil {
		st.QuotaStops = map[string]quotaStop{}
	}
	if st.QuotaResets == nil {
		st.QuotaResets = map[string]int64{}
	}
	changed := false
	for _, r := range pr.Rules {
		if r.QuotaReset != st.QuotaResets[r.Tag] {
			_, applied := st.Applied[r.Tag]
			_, configErr := os.Stat(filepath.Join(s.confDir, r.Tag+".conf"))
			if applied || configErr == nil {
				if err := s.mgr.StopRule(r.Tag); err != nil {
					log.Printf("[slave] 重置实例 %s 配额前停止失败: %v", r.Tag, err)
					continue
				}
			}
			if !s.run.DryRun {
				if err := rmFile(statsPath(r.Tag)); err != nil {
					log.Printf("[slave] 清除实例 %s 旧统计失败: %v", r.Tag, err)
					continue
				}
			}
			delete(st.QuotaStops, r.Tag)
			if applied {
				st.Applied[r.Tag] = -1
			}
			st.QuotaResets[r.Tag] = r.QuotaReset
			changed = true
		}
		if stop, ok := st.QuotaStops[r.Tag]; ok && (r.Quota <= 0 || uint64(r.Quota) > stop.Bytes) {
			delete(st.QuotaStops, r.Tag)
			changed = true
		}
	}
	return changed
}

// reconcile keeps failed changes pending so a later pull retries them. In
// particular, a failed deletion stays in Applied and is included in reports.
func (s *Slave) reconcile(pr *pullResponse, st *slaveState) bool {
	changed := false
	desired := map[string]nodeRulePayload{}
	for _, r := range pr.Rules {
		desired[r.Tag] = r
	}
	for tag := range st.Applied {
		if _, ok := desired[tag]; ok {
			continue
		}
		log.Printf("[slave] 移除实例 %s", tag)
		if err := s.mgr.DeleteUnit(tag); err != nil {
			log.Printf("[slave] 移除实例 %s 失败: %v", tag, err)
			continue
		}
		if !s.run.DryRun {
			if err := rmFile(filepath.Join(s.confDir, tag+".conf")); err != nil {
				log.Printf("[slave] 清理实例 %s 配置失败: %v", tag, err)
				continue
			}
			if err := rmFile(statsPath(tag)); err != nil {
				log.Printf("[slave] 清理实例 %s 统计失败: %v", tag, err)
				continue
			}
		}
		delete(st.Applied, tag)
		delete(st.QuotaStops, tag)
		delete(st.QuotaResets, tag)
		changed = true
	}
	for tag, r := range desired {
		if r.QuotaReset != st.QuotaResets[tag] {
			continue
		}
		if revision, ok := st.Applied[tag]; ok && revision == r.Revision {
			if _, err := os.Stat(filepath.Join(s.confDir, tag+".conf")); err == nil || s.run.DryRun {
				continue
			}
		}
		log.Printf("[slave] 应用实例 %s (rev=%d desired=%s)", tag, r.Revision, r.Desired)
		if !s.run.DryRun {
			if err := writeFileAtomic(filepath.Join(s.confDir, tag+".conf"), []byte(r.Env), 0o600); err != nil {
				log.Printf("[slave] 写入 %s.conf 失败: %v", tag, err)
				continue
			}
		}
		if _, exists := st.Applied[tag]; !exists {
			st.Applied[tag] = -1
			changed = true
		}
		_, blocked := st.QuotaStops[tag]
		var err error
		if r.Desired == "stopped" || blocked {
			err = s.mgr.StopRule(tag)
		} else {
			err = s.mgr.RestartRule(tag)
		}
		if err != nil {
			log.Printf("[slave] 应用实例 %s 失败: %v", tag, err)
			continue
		}
		st.Applied[tag] = r.Revision
		changed = true
	}
	return changed
}

func (s *Slave) collectStatuses(pr *pullResponse, st *slaveState) map[string]UnitStatus {
	statuses := map[string]UnitStatus{}
	for tag := range st.Applied {
		statuses[tag] = s.mgr.Status(tag)
	}
	for _, r := range pr.Rules {
		statuses[r.Tag] = s.mgr.Status(r.Tag)
	}
	return statuses
}

func (s *Slave) ensureRunning(pr *pullResponse, st *slaveState) {
	if s.proc == nil {
		return
	}
	for _, r := range pr.Rules {
		_, blocked := st.QuotaStops[r.Tag]
		revision, applied := st.Applied[r.Tag]
		if r.Desired != "stopped" && !blocked && applied && revision == r.Revision && r.QuotaReset == st.QuotaResets[r.Tag] {
			s.proc.EnsureAlive(r.Tag)
		}
	}
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

// enforceQuotas runs before reconcile/self-healing so stale or freshly reported
// excess usage cannot be erased by a process restart. Stops survive agent restarts.
func (s *Slave) enforceQuotas(pr *pullResponse, state *slaveState) (map[string]*RuleStats, bool) {
	stats := map[string]*RuleStats{}
	changed := false
	if state.QuotaStops == nil {
		state.QuotaStops = map[string]quotaStop{}
	}
	for _, r := range pr.Rules {
		st := ReadRuleStats(r.Tag)
		stop, blocked := state.QuotaStops[r.Tag]
		if r.Quota > 0 && st != nil && st.Total() >= uint64(r.Quota) {
			if !blocked || st.Total() > stop.Bytes {
				stop = quotaStop{Bytes: st.Total(), Reset: r.QuotaReset}
				state.QuotaStops[r.Tag] = stop
				changed = true
			}
			blocked = true
		}
		if blocked {
			if err := s.mgr.StopRule(r.Tag); err != nil {
				log.Printf("[slave] 超额实例 %s 停止失败，将重试: %v", r.Tag, err)
			}
			if st == nil {
				st = &RuleStats{Up: stop.Bytes}
			}
			st.OverQuota = true
		}
		if st != nil {
			st.QuotaReset = state.QuotaResets[r.Tag]
			stats[r.Tag] = st
		}
	}
	return stats, changed
}

func nodeProbeTarget(r nodeRulePayload) (string, int, bool, bool) {
	parsed := &Rule{EnableTCP: true, EnableUDP: true}
	for _, line := range strings.Split(r.Env, "\n") {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "REMOTE_IP":
			parsed.RemoteHost = kv[1]
		case "REMOTE_TCP_PORT":
			parsed.RemoteTCPPort, _ = strconv.Atoi(kv[1])
		case "REMOTE_UDP_PORT":
			parsed.RemoteUDPPort, _ = strconv.Atoi(kv[1])
		case "ENABLE_TCP":
			parsed.EnableTCP = kv[1] != "0"
		case "ENABLE_UDP":
			parsed.EnableUDP = kv[1] != "0"
		}
	}
	if parsed.RemoteHost == "" {
		return "", 0, false, false
	}
	return ruleProbeTarget(parsed)
}

// probeRemotes returns cached results immediately and starts at most one bounded
// batch per minute. Slow targets never block configuration pulls or quota checks.
func (s *Slave) probeRemotes(pr *pullResponse) map[string]*ProbeResult {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	out := map[string]*ProbeResult{}
	for _, r := range pr.Rules {
		if p := s.probeResults[r.Tag]; p != nil {
			cp := *p
			out[r.Tag] = &cp
		}
	}
	if s.run.DryRun || s.probeRunning || time.Since(s.probeLast) < time.Minute {
		return out
	}
	rules := append([]nodeRulePayload(nil), pr.Rules...)
	s.probeRunning = true
	s.probeLast = time.Now()
	go func() {
		results := map[string]*ProbeResult{}
		var mu sync.Mutex
		jobs := make(chan nodeRulePayload)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range jobs {
					host, port, soft, ok := nodeProbeTarget(r)
					if !ok {
						continue
					}
					result := s.probe(host, port, soft)
					mu.Lock()
					results[r.Tag] = result
					mu.Unlock()
				}
			}()
		}
		for _, r := range rules {
			jobs <- r
		}
		close(jobs)
		wg.Wait()
		s.probeMu.Lock()
		s.probeResults = results
		s.probeRunning = false
		s.probeMu.Unlock()
	}()
	return out
}

func (s *Slave) Run() {
	if err := s.EnsureEmbeddedTCPPool(); err != nil {
		log.Printf("[slave] %v", err)
	}
	st := s.loadState()
	if s.refreshKernelState(&st) {
		s.saveState(st)
	}
	tcpVer := s.tcpPoolVersion()
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
			// A missing master must not disable locally enforced quotas. Keep the
			// last desired set, but do not reconcile potentially stale changes.
			if _, changed := s.enforceQuotas(&pullResponse{Rules: st.Rules}, &st); changed {
				s.saveState(st)
			}
		} else {
			pr.Rules = validPulledRules(pr.Rules)
			if pr.Interval >= 1 && pr.Interval <= 60 {
				interval = time.Duration(pr.Interval) * time.Second
			}
			changed := s.syncQuotaState(&pr, &st)
			if !slices.Equal(st.Rules, pr.Rules) {
				st.Rules = append([]nodeRulePayload(nil), pr.Rules...)
				changed = true
			}
			stats, quotaChanged := s.enforceQuotas(&pr, &st)
			changed = quotaChanged || changed
			if s.reconcile(&pr, &st) {
				changed = true
			}
			if changed {
				s.saveState(st)
			}
			s.ensureRunning(&pr, &st)
			report := map[string]any{
				"machine":          ReadMachineInfo(),
				"tcp_pool_version": tcpVer,
				"panel_version":    panelVersion,
				"statuses":         s.collectStatuses(&pr, &st),
				"stats":            stats,
				"probes":           s.probeRemotes(&pr),
				"cleanup_complete": pr.Deleting && len(pr.Rules) == 0 && len(st.Applied) == 0,
				"error":            "",
			}
			if rerr := s.request("POST", "/api/node/report", report, nil); rerr != nil {
				log.Printf("[slave] 上报失败: %v", rerr)
			}
		}
		time.Sleep(interval)
	}
}

func (s *Slave) upgradeError() string {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	return s.upgrade.err
}

// checkUpgrade 在主循环每轮末尾调用：已有校验通过的新版本就安装并重启，否则按
// 主控给出的版本标签在后台下载。标签只用于拼出本机配置仓库的发布地址，且只接受
// 比当前更高的版本，主控无法借此让节点执行任意文件或降级。
func (s *Slave) checkUpgrade(target string) {
	u := s.updater
	if u == nil {
		return
	}
	s.upgradeMu.Lock()
	ready := s.upgrade.ready
	s.upgrade.ready = ""
	s.upgradeMu.Unlock()
	if ready != "" {
		s.applyUpgrade(ready)
		return
	}

	if target == "" {
		return
	}
	want, ok := parseSemver(target)
	if !ok || !releaseTagRe.MatchString(target) || !want.newerThan(u.current) {
		return // 标签不合法、已达到或高于目标：忽略
	}
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	if u.disabledReason != "" {
		s.upgrade.err = u.disabledReason
		return
	}
	if s.upgrade.running || (s.upgrade.tried == target && time.Since(s.upgrade.triedAt) < updateRetryAfter) {
		return
	}
	s.upgrade.running, s.upgrade.tried, s.upgrade.triedAt = true, target, time.Now()
	log.Printf("[slave] 主控要求升级到 %s，开始下载", target)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		tmp, err := u.Fetch(ctx, target)
		s.upgradeMu.Lock()
		defer s.upgradeMu.Unlock()
		s.upgrade.running = false
		if err != nil {
			log.Printf("[slave] 下载 %s 失败（%v 后重试）: %v", target, updateRetryAfter, err)
			s.upgrade.err = err.Error()
			return
		}
		s.upgrade.err = ""
		s.upgrade.ready = tmp
	}()
}

func (s *Slave) applyUpgrade(tmp string) {
	fail := func(err error) {
		log.Printf("[slave] 升级失败: %v", err)
		s.upgradeMu.Lock()
		s.upgrade.err = err.Error()
		s.upgradeMu.Unlock()
	}
	if err := s.updater.Install(tmp); err != nil {
		fail(err)
		return
	}
	log.Printf("[slave] 新版本已安装，重新启动代理")
	if s.proc != nil {
		s.proc.StopAll() // 直跑实例随代理重启；systemd 实例不受影响
	}
	if err := s.updater.restart(); err != nil {
		// 仍在运行旧代码；直跑实例由下一轮 ensureRunning 拉起
		fail(fmt.Errorf("新版本已安装，但重新启动失败，请手动重启节点服务: %w", err))
	}
}

func shellQuote(arg string) string { return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'" }

// A root-readable launcher preserves arguments exactly across both init systems,
// including certificate pinning and paths containing spaces or shell characters.
func (s *Slave) launchScript(binPath string) string {
	args := []string{binPath, "-slave", "-master", s.masterURL, "-node-key", s.nodeKey,
		"-conf-dir", s.confDir, "-tcpool-bin", s.binPath,
		"-state", filepath.Join(filepath.Dir(s.statePath), "panel.json")}
	if s.direct {
		args = append(args, "-direct")
	}
	if s.tlsFingerprint != "" {
		args = append(args, "-tls-fingerprint", s.tlsFingerprint)
	}
	if s.tlsInsecure {
		args = append(args, "-tls-insecure")
	}
	for i := range args {
		args[i] = shellQuote(args[i])
	}
	return "#!/bin/sh\nexec " + strings.Join(args, " ") + "\n"
}

// SelfInstall installs and starts the service; lack of a supported init system
// is reported instead of claiming the node has been started.
func (s *Slave) SelfInstall(binPath string) error {
	if err := s.EnsureEmbeddedTCPPool(); err != nil {
		return err
	}
	useSystemd := hasSystemd()
	if !useSystemd {
		for _, command := range []string{"rc-service", "rc-update"} {
			if _, err := exec.LookPath(command); err != nil {
				return fmt.Errorf("未找到 systemd 或 OpenRC（缺少 %s），请以 -slave 模式直接运行", command)
			}
		}
	}
	return s.installService(binPath, useSystemd, "")
}

// root is empty in production and allows service installation to be exercised in
// an isolated directory by tests without changing the host's init configuration.
func (s *Slave) installService(binPath string, useSystemd bool, root string) error {
	if root == "" {
		root = string(filepath.Separator)
	}
	launcher := filepath.Join(filepath.Dir(s.statePath), "slave-launch")
	if err := writeFileAtomic(launcher, []byte(s.launchScript(binPath)), 0o700); err != nil {
		return err
	}
	if !useSystemd {
		logDir := os.Getenv("PANEL_LOG_DIR")
		if logDir == "" {
			logDir = filepath.Join(root, "var/log/relay-panel")
		}
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return err
		}
		script := "#!/sbin/openrc-run\n" +
			"description=\"Relay Panel Slave Node Agent\"\n" +
			"command=" + shellQuote(launcher) + "\n" +
			"command_background=true\n" +
			"pidfile=" + shellQuote(filepath.Join(root, "run/relay-panel-slave.pid")) + "\n" +
			"output_log=" + shellQuote(filepath.Join(logDir, "slave.log")) + "\n" +
			"error_log=" + shellQuote(filepath.Join(logDir, "slave.log")) + "\n" +
			"depend() { need net; after firewall; }\n"
		if err := writeFileAtomic(filepath.Join(root, "etc/init.d/relay-panel-slave"), []byte(script), 0o700); err != nil {
			return err
		}
		if _, _, err := s.run.Run("rc-update", "add", "relay-panel-slave", "default"); err != nil {
			return err
		}
		if _, _, err := s.run.Run("rc-service", "relay-panel-slave", "restart"); err != nil {
			return err
		}
		log.Printf("[slave] 已安装并启动 OpenRC 服务 relay-panel-slave")
		return nil
	}
	paths := Paths{ConfDir: s.confDir, TCPPoolBin: s.binPath}
	if err := writeFileAtomic(filepath.Join(root, "etc/systemd/system/tcp-pool@.service"), []byte(serviceTemplateFor(paths)), 0o644); err != nil {
		return err
	}
	execPath := strconv.Quote(strings.ReplaceAll(strings.ReplaceAll(launcher, "%", "%%"), "$", "$$"))
	unit := "[Unit]\nDescription=Relay Panel Slave Node Agent\nWants=network-online.target\nAfter=network-online.target\n\n[Service]\nExecStart=" + execPath + "\nRestart=always\nRestartSec=5\n\n[Install]\nWantedBy=multi-user.target\n"
	if err := writeFileAtomic(filepath.Join(root, "etc/systemd/system/relay-panel-slave.service"), []byte(unit), 0o644); err != nil {
		return err
	}
	if _, _, err := s.run.Run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, _, err := s.run.Run("systemctl", "enable", "relay-panel-slave"); err != nil {
		return err
	}
	if _, _, err := s.run.Run("systemctl", "restart", "relay-panel-slave"); err != nil {
		return err
	}
	log.Printf("[slave] 已安装并启动 systemd 服务 relay-panel-slave")
	return nil
}
