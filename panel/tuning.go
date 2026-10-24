package main

import (
	"fmt"
	"os"
	"strings"
)

// tuningProfiles 三档 TCP 调优。所有档位在内核支持时都会启用 BBR + fq。
var tuningProfiles = map[string]struct {
	Label  string            `json:"label"`
	Desc   string            `json:"desc"`
	Sysctl map[string]string `json:"-"`
}{
	"conservative": {
		Label: "保守",
		Desc:  "小内存 VPS（<512M）适用，缓冲区占用低",
		Sysctl: map[string]string{
			"net.core.somaxconn":                 "16384",
			"net.ipv4.tcp_max_syn_backlog":       "16384",
			"net.core.netdev_max_backlog":        "16384",
			"net.core.rmem_max":                  "33554432",
			"net.core.wmem_max":                  "33554432",
			"net.core.rmem_default":              "262144",
			"net.core.wmem_default":              "262144",
			"net.ipv4.tcp_rmem":                  "4096 262144 33554432",
			"net.ipv4.tcp_wmem":                  "4096 262144 33554432",
			"net.ipv4.tcp_fastopen":              "3",
			"net.ipv4.tcp_fin_timeout":           "15",
			"net.ipv4.tcp_mtu_probing":           "1",
			"net.ipv4.tcp_slow_start_after_idle": "0",
			"net.ipv4.tcp_no_metrics_save":       "1",
			"net.ipv4.tcp_retries2":              "8",
			"net.ipv4.tcp_syncookies":            "1",
			"net.ipv4.ip_local_port_range":       "1024 65535",
		},
	},
	"standard": {
		Label: "标准",
		Desc:  "与原版 relay tune 相同的通用参数，适合绝大多数机器",
		Sysctl: map[string]string{
			"net.core.somaxconn":                 "65535",
			"net.ipv4.tcp_max_syn_backlog":       "65535",
			"net.core.netdev_max_backlog":        "250000",
			"net.core.rmem_max":                  "134217728",
			"net.core.wmem_max":                  "134217728",
			"net.core.rmem_default":              "1048576",
			"net.core.wmem_default":              "1048576",
			"net.ipv4.tcp_rmem":                  "4096 1048576 134217728",
			"net.ipv4.tcp_wmem":                  "4096 1048576 134217728",
			"net.ipv4.udp_rmem_min":              "8192",
			"net.ipv4.udp_wmem_min":              "8192",
			"net.ipv4.tcp_fastopen":              "3",
			"net.ipv4.tcp_fin_timeout":           "10",
			"net.ipv4.tcp_mtu_probing":           "1",
			"net.ipv4.tcp_slow_start_after_idle": "0",
			"net.ipv4.tcp_no_metrics_save":       "1",
			"net.ipv4.tcp_retries2":              "8",
			"net.ipv4.tcp_timestamps":            "1",
			"net.ipv4.tcp_sack":                  "1",
			"net.ipv4.tcp_syncookies":            "1",
			"net.ipv4.ip_local_port_range":       "1024 65535",
			"net.ipv4.tcp_keepalive_time":        "300",
			"net.ipv4.tcp_keepalive_intvl":       "15",
			"net.ipv4.tcp_keepalive_probes":      "2",
		},
	},
	"aggressive": {
		Label: "激进",
		Desc:  "大内存高带宽机器，缓冲区给到 256M，backlog 拉满",
		Sysctl: map[string]string{
			"net.core.somaxconn":                 "262144",
			"net.ipv4.tcp_max_syn_backlog":       "262144",
			"net.core.netdev_max_backlog":        "500000",
			"net.core.rmem_max":                  "268435456",
			"net.core.wmem_max":                  "268435456",
			"net.core.rmem_default":              "4194304",
			"net.core.wmem_default":              "4194304",
			"net.ipv4.tcp_rmem":                  "4096 4194304 268435456",
			"net.ipv4.tcp_wmem":                  "4096 4194304 268435456",
			"net.ipv4.udp_rmem_min":              "16384",
			"net.ipv4.udp_wmem_min":              "16384",
			"net.ipv4.tcp_fastopen":              "3",
			"net.ipv4.tcp_fin_timeout":           "10",
			"net.ipv4.tcp_mtu_probing":           "2",
			"net.ipv4.tcp_slow_start_after_idle": "0",
			"net.ipv4.tcp_no_metrics_save":       "1",
			"net.ipv4.tcp_retries2":              "8",
			"net.ipv4.tcp_timestamps":            "1",
			"net.ipv4.tcp_sack":                  "1",
			"net.ipv4.tcp_syncookies":            "1",
			"net.ipv4.ip_local_port_range":       "1024 65535",
			"net.ipv4.tcp_keepalive_time":        "300",
			"net.ipv4.tcp_keepalive_intvl":       "15",
			"net.ipv4.tcp_keepalive_probes":      "2",
			"net.ipv4.tcp_notsent_lowat":         "131072",
		},
	},
}

// 固定顺序输出，diff 友好
var tuningKeyOrder = []string{
	"net.core.somaxconn",
	"net.ipv4.tcp_max_syn_backlog",
	"net.core.netdev_max_backlog",
	"net.core.rmem_max",
	"net.core.wmem_max",
	"net.core.rmem_default",
	"net.core.wmem_default",
	"net.ipv4.tcp_rmem",
	"net.ipv4.tcp_wmem",
	"net.ipv4.udp_rmem_min",
	"net.ipv4.udp_wmem_min",
	"net.ipv4.tcp_fastopen",
	"net.ipv4.tcp_fin_timeout",
	"net.ipv4.tcp_mtu_probing",
	"net.ipv4.tcp_slow_start_after_idle",
	"net.ipv4.tcp_no_metrics_save",
	"net.ipv4.tcp_retries2",
	"net.ipv4.tcp_timestamps",
	"net.ipv4.tcp_sack",
	"net.ipv4.tcp_syncookies",
	"net.ipv4.ip_local_port_range",
	"net.ipv4.tcp_keepalive_time",
	"net.ipv4.tcp_keepalive_intvl",
	"net.ipv4.tcp_keepalive_probes",
	"net.ipv4.tcp_notsent_lowat",
}

type Tuner struct {
	run        *Runner
	sysctlFile string
	modulesDir string
}

func NewTuner(run *Runner, p Paths) *Tuner {
	return &Tuner{run: run, sysctlFile: p.SysctlFile, modulesDir: p.ModulesLoad}
}

// TuningStatus 面板系统页展示的当前内核网络状态。
type TuningStatus struct {
	Kernel            string        `json:"kernel"`
	CongestionControl string        `json:"congestion_control"`
	AvailableCC       []string      `json:"available_cc"`
	QDisc             string        `json:"qdisc"`
	BBRSupported      bool          `json:"bbr_supported"`
	BBRActive         bool          `json:"bbr_active"`
	ConfigFile        string        `json:"config_file"`
	FileManaged       bool          `json:"file_managed"`
	FileExists        bool          `json:"file_exists"`
	Profiles          []ProfileInfo `json:"profiles"`
}

type ProfileInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Desc  string `json:"desc"`
}

func readProc(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (t *Tuner) Status() TuningStatus {
	st := TuningStatus{
		Kernel:            readProc("/proc/sys/kernel/osrelease"),
		CongestionControl: readProc("/proc/sys/net/ipv4/tcp_congestion_control"),
		ConfigFile:        t.sysctlFile,
	}
	st.QDisc = readProc("/proc/sys/net/core/default_qdisc")
	avail := readProc("/proc/sys/net/ipv4/tcp_available_congestion_control")
	st.AvailableCC = strings.Fields(avail)
	for _, cc := range st.AvailableCC {
		if cc == "bbr" {
			st.BBRSupported = true
		}
	}
	st.BBRActive = st.CongestionControl == "bbr"
	if data, err := os.ReadFile(t.sysctlFile); err == nil {
		st.FileExists = true
		st.FileManaged = strings.Contains(string(data), "relay-panel")
	}
	for _, name := range []string{"conservative", "standard", "aggressive"} {
		p := tuningProfiles[name]
		st.Profiles = append(st.Profiles, ProfileInfo{Name: name, Label: p.Label, Desc: p.Desc})
	}
	return st
}

// ApplyBBR 开启 BBR：加载 tcp_bbr 模块并设置拥塞控制与 qdisc。
func (t *Tuner) ApplyBBR() error {
	avail := readProc("/proc/sys/net/ipv4/tcp_available_congestion_control")
	if !strings.Contains(avail, "bbr") {
		// 尝试显式加载模块；OpenVZ 等容器内核可能不支持
		if _, _, err := t.run.Run("modprobe", "tcp_bbr"); err != nil && !t.run.DryRun {
			return fmt.Errorf("内核不支持 BBR（tcp_available_congestion_control 中没有 bbr，modprobe 失败）")
		}
	}
	if !t.run.DryRun {
		if err := writeFileAtomic(t.modulesDir+"/tcp-bbr.conf", []byte("tcp_bbr\n"), 0o644); err != nil {
			return err
		}
		_, _, _ = t.run.Run("modprobe", "tcp_bbr")
	}
	if _, _, err := t.run.Run("sysctl", "-w", "net.ipv4.tcp_congestion_control=bbr"); err != nil && !t.run.DryRun {
		return fmt.Errorf("设置 BBR 失败（内核可能未内置支持）")
	}
	_, _, _ = t.run.Run("sysctl", "-w", "net.core.default_qdisc=fq")
	return nil
}

// ApplyProfile 应用一档调优并即时生效。bbr=true 时同时确保 BBR 开启。
func (t *Tuner) ApplyProfile(name string, bbr bool) error {
	prof, ok := tuningProfiles[name]
	if !ok {
		return fmt.Errorf("未知调优档位: %s", name)
	}

	var b strings.Builder
	b.WriteString("# relay-panel TCP 调优 (" + name + " / " + prof.Label + ")\n")
	b.WriteString("# 面板管理文件，手动修改会在下次调优时被覆盖\n")
	cc := "cubic"
	if bbr {
		cc = "bbr"
	}
	fmt.Fprintf(&b, "net.ipv4.tcp_congestion_control = %s\n", cc)
	b.WriteString("net.core.default_qdisc = fq\n")
	for _, k := range tuningKeyOrder {
		if v, ok := prof.Sysctl[k]; ok {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}

	if !t.run.DryRun {
		if bbr {
			if err := t.ApplyBBR(); err != nil {
				return err
			}
		}
		if err := writeFileAtomic(t.sysctlFile, []byte(b.String()), 0o644); err != nil {
			return err
		}
	}
	if _, _, err := t.run.Run("sysctl", "--system"); err != nil && !t.run.DryRun {
		return fmt.Errorf("sysctl --system 应用失败，配置文件已写入 %s", t.sysctlFile)
	}
	return nil
}

// MachineInfo 面板首页的机器概览。
type MachineInfo struct {
	Kernel   string `json:"kernel"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	CPUCores int    `json:"cpu_cores"`
	LoadAvg  string `json:"load_avg"`
	MemTotal int64  `json:"mem_total"`
	MemAvail int64  `json:"mem_avail"`
}

func ReadMachineInfo() MachineInfo {
	info := MachineInfo{
		Kernel:   readProc("/proc/sys/kernel/osrelease"),
		Hostname: readProc("/proc/sys/kernel/hostname"),
	}
	if data, err := os.ReadFile("/proc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
			}
		}
	}
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "processor") {
				info.CPUCores++
			}
		}
	}
	if load := readProc("/proc/loadavg"); load != "" {
		fields := strings.Fields(load)
		if len(fields) >= 3 {
			info.LoadAvg = strings.Join(fields[:3], " ")
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			var kb int64
			fmt.Sscanf(fields[1], "%d", &kb)
			switch fields[0] {
			case "MemTotal:":
				info.MemTotal = kb * 1024
			case "MemAvailable:":
				info.MemAvail = kb * 1024
			}
		}
	}
	return info
}
