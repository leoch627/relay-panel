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
		"traffic_total": trafficTotal,
		"over_quota":    overQuota,
		"version":       panelVersion,
	}
	// 主控主机信息、调优状态与用户总数只对管理员展示
	if u.IsAdmin {
		resp["users_total"] = len(a.store.ListUsers())
		resp["machine"] = ReadMachineInfo()
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

	// done 只统计真正生效的规则；失败原因记录第一条返回给前端，避免静默跳过
	done, skipped := 0, 0
	var firstErr string
	fail := func(tag string, err error) {
		skipped++
		if firstErr == "" {
			firstErr = tag + "：" + err.Error()
		}
	}
	for _, tag := range req.Tags {
		rl := a.store.GetRule(tag)
		if rl == nil || (!u.IsAdmin && rl.OwnerID != u.ID) {
			skipped++ // 不存在或无权操作：不暴露原因
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
					err = a.sd.RestartRuleFresh(tag)
				} else {
					err = a.sd.StopRule(tag)
				}
				if err != nil {
					fail(tag, err)
					continue
				}
			}
			if err := a.store.SetRuleDesiredState(tag, desired); err != nil {
				fail(tag, err)
				continue
			}
		case "delete":
			deleted, err := a.deleteRule(rl)
			if err != nil && !deleted {
				fail(tag, err)
				continue
			}
			if err != nil && firstErr == "" {
				firstErr = tag + "：" + err.Error() // 已删除，仅配置文件清理失败
			}
		}
		done++
	}
	if req.Action == "delete" {
		a.sd.DaemonReload()
	}
	writeJSON(w, 200, map[string]any{"done": done, "skipped": skipped, "error": firstErr})
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
		Rules  []ruleDTO `json:"rules"`
		NodeID *int64    `json:"node_id"` //仅显式指定时覆盖每条规则的节点
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
		//导入的规则归属当前管理员；目标节点可选
		if req.NodeID != nil {
			dto.NodeID = *req.NodeID
		}
		rl, _, msg := a.checkRule(&dto, "")
		if rl == nil {
			failed++
			if firstErr == "" {
				firstErr = msg
				if dto.Tag != "" {
					firstErr = dto.Tag + "：" + msg
				}
			}
			continue
		}
		rl.OwnerID = u.ID
		if err := a.store.CreateRule(rl); err != nil {
			if err == ErrDuplicate {
				skipped++
				continue
			}
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		if err := a.applyRule(rl, rl.NodeID); err != nil {
			failed++
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		imported++
	}
	writeJSON(w, 200, map[string]any{
		"imported": imported, "skipped": skipped, "failed": failed, "error": firstErr,
	})
}

// ---------- TLS 信息（节点安装命令引用指纹） ----------

func (a *API) handleTLSInfo(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{
		"enabled":     a.tls != nil && a.tls.Enabled,
		"fingerprint": a.tlsFingerprint(),
		"generated":   a.tls != nil && a.tls.Generated,
		"pin_recommended": a.tls != nil && a.tls.Generated && r.TLS != nil &&
			r.Header.Get("Forwarded") == "" && r.Header.Get("X-Forwarded-Proto") == "" &&
			r.Header.Get("X-Forwarded-Host") == "" && r.Header.Get("Cf-Ray") == "",
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
