package main

import (
	"encoding/json"
	"os"
	"time"
)

// RuleStats 底层 tcp_pool 周期写出的统计快照（/run/tcp-pool/<tag>.stats.json）。
// 计数随实例重启归零；up/down 已合并 UDP 字节。
type RuleStats struct {
	Up         uint64 `json:"up"`          //客户端->远端累计字节
	Down       uint64 `json:"down"`        //远端->客户端累计字节
	Conns      int    `json:"conns"`       //当前活跃TCP连接
	ConnsTotal uint64 `json:"conns_total"` //历史TCP连接数
	UDP        int    `json:"udp"`         //当前UDP会话
	UDPTotal   uint64 `json:"udp_total"`   //历史UDP会话
	RateKBps   int64  `json:"rate_kbps"`
	T          int64  `json:"t"` //快照unix秒
	//以下由面板计算填充，不来自文件
	OverQuota bool `json:"over_quota,omitempty"`
}

// statsDir 可用 PANEL_STATS_DIR 覆盖（默认与 C 程序约定路径一致）
var statsDir = func() string {
	if d := os.Getenv("PANEL_STATS_DIR"); d != "" {
		return d
	}
	return "/run/tcp-pool"
}()

func statsPath(tag string) string { return statsDir + "/" + tag + ".stats.json" }

// ReadRuleStats 读取统计快照；文件缺失/损坏返回 nil（未启动或旧版内核）。
func ReadRuleStats(tag string) *RuleStats {
	data, err := os.ReadFile(statsPath(tag))
	if err != nil {
		return nil
	}
	var s RuleStats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil
	}
	return &s
}

func (s *RuleStats) Total() uint64 {
	if s == nil {
		return 0
	}
	return s.Up + s.Down
}

// ---------- 配额执行 ----------

// EnforceLocalQuotas 主控定时巡检本机规则配额：超限即停机并标记。
// 由 main 里起的 goroutine 每 15s 调用一次。
func EnforceLocalQuotas(store *Store, sd *SystemdManager) {
	for _, r := range store.ListRules(0, true) {
		if r.NodeID != 0 || r.TrafficQuotaBytes <= 0 {
			continue
		}
		st := ReadRuleStats(r.Tag)
		if st == nil {
			continue
		}
		if st.Total() >= uint64(r.TrafficQuotaBytes) {
			_ = sd.StopRule(r.Tag)
			store.SetRuleOverQuota(r.Tag, true)
		}
	}
}

// StartQuotaEnforcer 阻塞循环。
func StartQuotaEnforcer(store *Store, sd *SystemdManager, dryRun bool) {
	if dryRun {
		return
	}
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		EnforceLocalQuotas(store, sd)
	}
}
