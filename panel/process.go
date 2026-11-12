package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RuleRunner 是管理 tcp_pool 实例的统一接口，systemd 与直跑两种实现共用。
type RuleRunner interface {
	RestartRule(tag string) error
	StopRule(tag string) error
	DeleteUnit(tag string) error
	Status(tag string) UnitStatus
	Logs(tag string, lines int) (string, error)
}

// ProcessManager 直跑模式：tcp_pool 作为本进程子进程运行，不依赖 systemd。
// 适用 Alpine（OpenRC）或无 systemd 环境；日志写 /var/log/relay-panel/<tag>.log。
type ProcessManager struct {
	mu       sync.Mutex
	run      *Runner
	confDir  string
	binPath  string
	logDir   string
	children map[string]*procEntry
}

type procEntry struct {
	cmd      *exec.Cmd
	started  time.Time
	restarts int
	done     chan struct{}
}

func (e *procEntry) alive() bool {
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

func NewProcessManager(run *Runner, p Paths) *ProcessManager {
	logDir := os.Getenv("PANEL_LOG_DIR")
	if logDir == "" {
		logDir = "/var/log/relay-panel"
	}
	return &ProcessManager{
		run:      run,
		confDir:  p.ConfDir,
		binPath:  p.TCPPoolBin,
		logDir:   logDir,
		children: map[string]*procEntry{},
	}
}

// loadEnvFile 解析 systemd EnvironmentFile 格式（KEY=VAL 每行）。
func loadEnvFile(path string) []string {
	env := []string{}
	f, err := os.Open(path)
	if err != nil {
		return env
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			env = append(env, line)
		}
	}
	return env
}

func (m *ProcessManager) logFile(tag string) string {
	return m.logDir + "/" + tag + ".log"
}

func (m *ProcessManager) startLocked(tag string) error {
	if m.run.DryRun {
		fmt.Printf("[dry-run] spawn %s --config %s/%s.conf\n", m.binPath, m.confDir, tag)
		return nil
	}
	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录 %s 失败: %w", m.logDir, err)
	}
	logf, err := os.OpenFile(m.logFile(tag), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(m.binPath)
	cmd.Env = append(os.Environ(), loadEnvFile(m.confDir+"/"+tag+".conf")...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return err
	}
	entry := &procEntry{cmd: cmd, started: time.Now(), done: make(chan struct{})}
	m.children[tag] = entry
	// 回收退出进程，避免僵尸；异常退出留在 map 里由 ensureAlive 处理
	go func() {
		_ = cmd.Wait()
		logf.Close()
		close(entry.done)
	}()
	return nil
}

func (m *ProcessManager) killLocked(tag string) error {
	if e, ok := m.children[tag]; ok {
		if e.alive() {
			if err := e.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return err
			}
			select {
			case <-e.done:
			case <-time.After(3 * time.Second):
				if err := e.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					return err
				}
				select {
				case <-e.done:
				case <-time.After(3 * time.Second):
					return fmt.Errorf("等待实例 %s 退出超时", tag)
				}
			}
		}
		delete(m.children, tag)
	}
	return nil
}

func (m *ProcessManager) RestartRule(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.killLocked(tag); err != nil {
		return err
	}
	return m.startLocked(tag)
}

func (m *ProcessManager) StopRule(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.killLocked(tag)
}

func (m *ProcessManager) DeleteUnit(tag string) error {
	return m.StopRule(tag)
}

func (m *ProcessManager) Status(tag string) UnitStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.children[tag]; ok && e.alive() {
		st := UnitStatus{
			Active:    "active",
			Enabled:   "enabled",
			MainPID:   fmt.Sprintf("%d", e.cmd.Process.Pid),
			StartedAt: e.started.Format("2006-01-02 15:04:05"),
			Restarts:  fmt.Sprintf("%d", e.restarts),
		}
		return st
	}
	return UnitStatus{Active: "inactive", Enabled: "unknown"}
}

// Alive 直跑实例当前是否存活（ slave 自愈巡检用）。
func (m *ProcessManager) Alive(tag string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.children[tag]
	return ok && e.alive()
}

// EnsureAlive 期望 running 的实例若不在（崩溃/被杀）则重启；带最短间隔防抖。
func (m *ProcessManager) EnsureAlive(tag string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.children[tag]
	if ok && e.alive() {
		return
	}
	if ok && time.Since(e.started) < 5*time.Second {
		return // 启动即崩的兜底节流
	}
	if err := m.killLocked(tag); err != nil {
		return
	}
	if err := m.startLocked(tag); err == nil && ok {
		if ne := m.children[tag]; ne != nil {
			ne.restarts = e.restarts + 1
		}
	}
}

func (m *ProcessManager) Logs(tag string, lines int) (string, error) {
	if m.run.DryRun {
		return "[dry-run] cat " + m.logFile(tag) + "\n", nil
	}
	data, err := os.ReadFile(m.logFile(tag))
	if err != nil {
		if os.IsNotExist(err) {
			return "（无日志）", nil
		}
		return "", err
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}

// hasSystemd 探测本机是否有可用的 systemd（无则 slave 走直跑模式）。
func hasSystemd() bool {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false
	}
	_, err := exec.LookPath("systemctl")
	return err == nil
}
