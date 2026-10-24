package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// 被控节点轮询配置下发周期（秒）
const nodePullIntervalSec = 5

// ---------- 被控机端点（Bearer 节点密钥认证） ----------

func (a *API) nodeAuth(r *http.Request) *Node {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil
	}
	return a.store.GetNodeByKey(strings.TrimPrefix(h, "Bearer "))
}

type nodeRulePayload struct {
	Tag      string `json:"tag"`
	Revision int64  `json:"revision"`
	Desired  string `json:"desired"`
	Env      string `json:"env"`
	Quota    int64  `json:"quota"` //流量配额字节，0不限；节点本地执行超限停机
}

// handleNodePull 被控机拉取自己的期望规则集（全量下发，被控端做差量 reconcile）。
func (a *API) handleNodePull(w http.ResponseWriter, r *http.Request) {
	node := a.nodeAuth(r)
	if node == nil {
		errJSON(w, 401, "节点密钥无效")
		return
	}
	rules := a.store.RulesOnNode(node.ID)
	payload := make([]nodeRulePayload, 0, len(rules))
	for _, rl := range rules {
		desired := rl.DesiredState
		if desired == "" {
			desired = "running"
		}
		payload = append(payload, nodeRulePayload{
			Tag:      rl.Tag,
			Revision: rl.UpdatedAt.UnixMilli(),
			Desired:  desired,
			Env:      a.sd.EnvFileContent(rl),
			Quota:    rl.TrafficQuotaBytes,
		})
	}
	writeJSON(w, 200, map[string]any{
		"node_id":  node.ID,
		"interval": nodePullIntervalSec,
		"rules":    payload,
	})
}

// handleNodeReport 被控机上报各实例状态与机器信息。
func (a *API) handleNodeReport(w http.ResponseWriter, r *http.Request) {
	node := a.nodeAuth(r)
	if node == nil {
		errJSON(w, 401, "节点密钥无效")
		return
	}
	var req struct {
		Machine       MachineInfo           `json:"machine"`
		TCPoolVersion string                `json:"tcp_pool_version"`
		PanelVersion  string                `json:"panel_version"`
		Statuses      map[string]UnitStatus `json:"statuses"`
		Stats         map[string]*RuleStats `json:"stats"`
		Error         string                `json:"error"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	//节点上报的超限信息直接落到规则上（节点已在本地停机）
	for tag, st := range req.Stats {
		if st != nil && st.OverQuota {
			a.store.SetRuleOverQuota(tag, true)
		}
	}
	rep := NodeReport{
		At:            time.Now(),
		Machine:       req.Machine,
		TCPoolVersion: req.TCPoolVersion,
		PanelVersion:  req.PanelVersion,
		Statuses:      req.Statuses,
		Stats:         req.Stats,
		Error:         req.Error,
	}
	if rep.Statuses == nil {
		rep.Statuses = map[string]UnitStatus{}
	}
	data, _ := json.Marshal(&rep)
	a.store.TouchNode(node.ID, string(data))
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// ---------- 管理端点 ----------

func (a *API) handleNodesList(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	nodes := a.store.ListNodes()
	ruleCount := map[int64]int{}
	for _, rl := range a.store.ListRules(0, true) {
		ruleCount[rl.NodeID]++
	}
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		rep := ParseNodeReport(n)
		row := map[string]any{
			"id":         n.ID,
			"name":       n.Name,
			"online":     NodeOnline(n),
			"last_seen":  isoTime(n.LastSeen),
			"rule_count": ruleCount[n.ID],
			"report": map[string]any{
				"at":       isoTime(rep.At),
				"machine":  rep.Machine,
				"tcp_pool": rep.TCPoolVersion,
				"panel":    rep.PanelVersion,
				"statuses": rep.Statuses,
			},
		}
		if u.IsAdmin {
			row["key"] = n.Key // 密钥仅管理员可见
		}
		out = append(out, row)
	}
	writeJSON(w, 200, map[string]any{"nodes": out})
}

func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (a *API) handleNodeCreate(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	n, err := a.store.CreateNode(req.Name)
	if err == ErrDuplicate {
		errJSON(w, 409, "节点名已存在")
		return
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"node": map[string]any{
		"id": n.ID, "name": n.Name, "key": n.Key,
	}})
}

func (a *API) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		errJSON(w, 400, "节点 ID 不合法")
		return
	}
	if a.store.GetNode(id) == nil {
		errJSON(w, 404, "节点不存在")
		return
	}
	// 级联删除该节点名下规则；节点已失联时其机器上的实例需人工清理
	n := a.store.DeleteRulesByNode(id)
	if err := a.store.DeleteNode(id); err != nil {
		errJSON(w, 500, "删除节点失败: "+err.Error())
		return
	}
	a.sd.SyncAll(a.store.ListRules(0, true))
	writeJSON(w, 200, map[string]any{"ok": "1", "rules_removed": n})
}

func (a *API) handleNodeRegenKey(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		errJSON(w, 400, "节点 ID 不合法")
		return
	}
	n, err := a.store.RegenerateNodeKey(id)
	if err == ErrNotFound {
		errJSON(w, 404, "节点不存在")
		return
	}
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"key": n.Key})
}
