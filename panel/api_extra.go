package main

import (
	"net/http"
	"time"
)

// ---------- 仪表盘 ----------

func (a *API) handleOverview(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	rules := a.store.ListRules(u.ID, u.IsAdmin)

	nodesTotal, nodesOnline := 0, 0
	for _, n := range a.store.ListNodes() {
		nodesTotal++
		if NodeOnline(n) {
			nodesOnline++
		}
	}

	running, stopped := 0, 0
	var trafficTotal uint64
	var overQuota []string
	nodeReports := map[int64]*NodeReport{}
	for _, n := range a.store.ListNodes() {
		nodeReports[n.ID] = ParseNodeReport(n)
	}
	for _, rl := range rules {
		if rl.OverQuota {
			overQuota = append(overQuota, rl.Tag)
		}
		switch rl.DesiredState {
		case "stopped":
			stopped++
		default:
			running++
		}
		var st *RuleStats
		if rl.NodeID == 0 {
			st = ReadRuleStats(rl.Tag)
		} else if rep, ok := nodeReports[rl.NodeID]; ok && rep.Stats != nil {
			st = rep.Stats[rl.Tag]
		}
		if st != nil {
			trafficTotal += st.Total()
		}
	}

	resp := map[string]any{
		"rules_total":   len(rules),
		"rules_running": running,
		"rules_stopped": stopped,
		"nodes_total":   nodesTotal,
		"nodes_online":  nodesOnline,
		"users_total":   len(a.store.ListUsers()),
		"traffic_total": trafficTotal,
		"over_quota":    overQuota,
		"machine":       ReadMachineInfo(),
		"version":       panelVersion,
	}
	if u.IsAdmin {
		resp["tuning"] = a.tuner.Status()
	}
	writeJSON(w, 200, resp)
}

// ---------- 批量操作 ----------

func (a *API) handleRulesBatch(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	var req struct {
		Tags   []string `json:"tags"`
		Action string   `json:"action"` // start | stop | delete
	}
	if err := readBody(r, &req); err != nil || len(req.Tags) == 0 {
		errJSON(w, 400, "请求格式错误或未选择规则")
		return
	}
	if req.Action != "start" && req.Action != "stop" && req.Action != "delete" {
		errJSON(w, 400, "未知操作")
		return
	}

	done, skipped := 0, 0
	for _, tag := range req.Tags {
		rl := a.store.GetRule(tag)
		if rl == nil {
			skipped++
			continue
		}
		if !u.IsAdmin && rl.OwnerID != u.ID {
			skipped++
			continue
		}
		switch req.Action {
		case "start", "stop":
			desired := "running"
			if req.Action == "stop" {
				desired = "stopped"
			}
			if rl.NodeID == 0 {
				var err error
				if desired == "running" {
					err = a.sd.RestartRule(tag)
				} else {
					err = a.sd.StopRule(tag)
				}
				if err != nil {
					skipped++
					continue
				}
			}
			_ = a.store.SetRuleDesiredState(tag, desired)
			if desired == "running" {
				a.store.SetRuleOverQuota(tag, false)
			}
		case "delete":
			if _, err := a.store.DeleteRule(tag); err != nil {
				skipped++
				continue
			}
			if rl.NodeID == 0 {
				_ = a.sd.DeleteUnit(tag)
			}
		}
		done++
	}
	if req.Action == "delete" {
		_ = a.sd.RemoveRuleFiles(a.store.ListRules(0, true), "")
		a.sd.DaemonReload()
	}
	writeJSON(w, 200, map[string]any{"done": done, "skipped": skipped})
}

// ---------- 导入 / 导出 ----------

func (a *API) handleRulesExport(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rules := a.store.ListRules(0, true)
	out := make([]ruleDTO, 0, len(rules))
	for _, rl := range rules {
		out = append(out, ruleDTO{
			Tag: rl.Tag, ListenIP: rl.ListenIP, ListenPort: rl.ListenPort,
			RemoteHost: rl.RemoteHost, RemoteTCPPort: rl.RemoteTCPPort, RemoteUDPPort: rl.RemoteUDPPort,
			EnableTCP: rl.EnableTCP, EnableUDP: rl.EnableUDP, Advanced: rl.Advanced,
			Remark: rl.Remark, NodeID: rl.NodeID,
			RateLimitKBps: rl.RateLimitKBps, TrafficQuotaBytes: rl.TrafficQuotaBytes,
		})
	}
	writeJSON(w, 200, map[string]any{"rules": out, "exported_at": time.Now().Format(time.RFC3339)})
}

func (a *API) handleRulesImport(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Rules []ruleDTO `json:"rules"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	var imported, skipped, failed int
	var firstErr string
	for i := range req.Rules {
		dto := req.Rules[i]
		if a.store.GetRule(dto.Tag) != nil {
			skipped++ //已存在跳过，不覆盖线上配置
			continue
		}
		//导入的规则归属当前管理员
		dto.NodeID = 0 //节点依赖环境，导入一律落到本机，可再编辑迁移
		w2 := &dummyWriter{code: 200}
		rl := a.validateRule(w2, &dto, "")
		if w2.code != 200 || rl == nil {
			failed++
			if firstErr == "" {
				firstErr = w2.msg
			}
			continue
		}
		rl.OwnerID = u.ID
		if err := a.store.UpsertRule(rl); err != nil {
			failed++
			continue
		}
		if err := a.applyRule(rl, 0); err != nil {
			failed++
			continue
		}
		imported++
	}
	writeJSON(w, 200, map[string]any{
		"imported": imported, "skipped": skipped, "failed": failed, "error": firstErr,
	})
}

// dummyWriter 让 validateRule 复用错误输出而不真正写响应
type dummyWriter struct {
	code int
	msg  string
}

func (d *dummyWriter) Header() http.Header { return http.Header{} }
func (d *dummyWriter) Write(b []byte) (int, error) {
	if d.msg == "" {
		d.msg = string(b)
	}
	return len(b), nil
}
func (d *dummyWriter) WriteHeader(code int) { d.code = code }

// ---------- TLS 信息（节点安装命令引用指纹） ----------

func (a *API) handleTLSInfo(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled":     a.tls != nil && a.tls.Enabled,
		"fingerprint": a.tlsFingerprint(),
		"generated":   a.tls != nil && a.tls.Generated,
	})
}

// ---------- 流量统计 ----------

func (a *API) handleTraffic(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	if a.traffic == nil {
		errJSON(w, 503, "流量采样器未启动")
		return
	}
	writeJSON(w, 200, a.traffic.Query(r.URL.Query().Get("range"), u))
}
