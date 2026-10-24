package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Paths 集中管理面板用到的所有文件路径，便于测试和自定义安装位置。
type Paths struct {
	StateFile   string // 面板数据（用户/规则/会话）
	ConfDir     string // 底层配置目录，默认 /etc/tcp_pool
	RelaysConf  string // 底层主配置文件 relays.conf
	TCPPoolBin  string // 底层 C 程序
	SysctlFile  string // 调优 sysctl 配置
	ModulesLoad string // 开机加载模块目录
}

func DefaultPaths() Paths {
	return Paths{
		StateFile:   "/etc/relay-panel/panel.json",
		ConfDir:     "/etc/tcp_pool",
		RelaysConf:  "/etc/tcp_pool/relays.conf",
		TCPPoolBin:  "/root/tcp_pool",
		SysctlFile:  "/etc/sysctl.d/99-custom-network-tuning.conf",
		ModulesLoad: "/etc/modules-load.d",
	}
}

// Runner 封装外部命令执行；DryRun 模式只打印不执行，用于本地开发与演练。
type Runner struct {
	DryRun bool
}

func (r *Runner) Run(name string, args ...string) (string, string, error) {
	if r.DryRun {
		fmt.Printf("[dry-run] %s %s\n", name, strings.Join(args, " "))
		return "", "", nil
	}
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// writeFileAtomic 临时文件 + rename 原子写，避免写一半被读到。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后是空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

var (
	tagRe        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	remoteHostRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)
)

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validLocalIP(s string) bool {
	if s == "" {
		return false
	}
	return net.ParseIP(s) != nil
}

func isWildcardIP(s string) bool { return s == "0.0.0.0" || s == "::" }

func isIPv6(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() == nil
}

func rmFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func parseID(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

// advancedSpec 与底层 tcp_pool.c 的环境变量取值范围保持一致（见 tcp-pool-parse）。
var advancedSpec = map[string][2]int{
	"POOL_SIZE":           {0, 256},
	"REFILL_BATCH":        {1, 256},
	"CONNECT_TIMEOUT":     {1, 120},
	"IDLE_TIMEOUT":        {30, 86400},
	"HALF_CLOSE_TIMEOUT":  {1, 300},
	"PRECONNECT_TTL_MS":   {10000, 3600000},
	"SPLICE_CHUNK":        {16384, 1048576},
	"UDP_IDLE_TIMEOUT":    {5, 3600},
	"UDP_SOCKET_BUFFER":   {65536, 67108864},
	"LISTEN_BACKLOG":      {128, 65535},
	"LOG_ENABLE":          {0, 1},
	"LOG_RATE_PER_SEC":    {0, 10000},
	"TCP_KEEPIDLE":        {30, 86400},
	"TCP_KEEPINTVL":       {1, 3600},
	"TCP_KEEPCNT":         {1, 30},
	"TCP_USER_TIMEOUT_MS": {0, 3600000},
}

// advancedOrder 决定写配置文件时的输出顺序，保持稳定。
var advancedOrder = []string{
	"POOL_SIZE", "REFILL_BATCH", "CONNECT_TIMEOUT", "IDLE_TIMEOUT",
	"HALF_CLOSE_TIMEOUT", "PRECONNECT_TTL_MS", "SPLICE_CHUNK",
	"UDP_IDLE_TIMEOUT", "UDP_SOCKET_BUFFER", "LISTEN_BACKLOG",
	"LOG_ENABLE", "LOG_RATE_PER_SEC", "TCP_KEEPIDLE", "TCP_KEEPINTVL",
	"TCP_KEEPCNT", "TCP_USER_TIMEOUT_MS",
}
