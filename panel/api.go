package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// panelSemver 是版本号的唯一来源：发布标签必须是 "v"+panelSemver（CI 会校验），
// 在线升级据此比较新旧。
const panelSemver = "1.2.0"

const panelVersion = "relay-panel v" + panelSemver + " (tcp_pool v1.8)"

type API struct {
	store   *Store
	sd      *SystemdManager
	tuner   *Tuner
	run     *Runner
	tls     *TLSConfig
	traffic *TrafficCollector
	prober  *Prober
	updater *Updater
}

func (a *API) tlsFingerprint() string {
	if a.tls == nil {
		return ""
	}
	return a.tls.Fingerprint
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (a *API) currentUser(r *http.Request) *User {
	c, err := r.Cookie("panel_session")
	if err != nil || c.Value == "" {
		return nil
	}
	return a.store.Auth(c.Value)
}

func (a *API) requireAuth(w http.ResponseWriter, r *http.Request) (*User, string, bool) {
	c, err := r.Cookie("panel_session")
	if err != nil || c.Value == "" {
		errJSON(w, http.StatusUnauthorized, "未登录")
		return nil, "", false
	}
	u := a.store.Auth(c.Value)
	if u == nil {
		errJSON(w, http.StatusUnauthorized, "会话已失效，请重新登录")
		return nil, "", false
	}
	return u, c.Value, true
}

func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) (*User, string, bool) {
	u, tok, ok := a.requireAuth(w, r)
	if !ok {
		return nil, "", false
	}
	if !u.IsAdmin {
		errJSON(w, http.StatusForbidden, "需要管理员权限")
		return nil, "", false
	}
	return u, tok, true
}

func readBody(r *http.Request, v any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("请求体为空")
	}
	return json.Unmarshal(data, v)
}

// ---------- auth ----------

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	token, u, err := a.store.Login(req.Username, req.Password, loginClientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, ErrBadAuth):
			errJSON(w, 401, "用户名或密码错误")
		case errors.Is(err, ErrLoginLocked):
			errJSON(w, http.StatusTooManyRequests, err.Error())
		default:
			//禁用等带具体原因的错误直接透传（仅在密码正确时出现）
			errJSON(w, 401, err.Error())
		}
		return
	}
	//反代/CDN 场景：直连 TLS 或 X-Forwarded-Proto=https 时标记 Secure
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   7 * 24 * 3600,
	})
	writeJSON(w, 200, map[string]any{"user": publicUser(u)})
}

// loginClientIP 返回登录限流使用的客户端地址。直连对端是本机或内网地址时
// （面板前面有 nginx / Caddy 等反向代理的常见部署），采用代理追加在
// X-Forwarded-For 最右侧的地址；公网直连时忽略这些头，防止伪造绕过限流。
func loginClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil || !(peer.IsLoopback() || peer.IsPrivate()) {
		return host
	}
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		parts := strings.Split(values[len(values)-1], ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return host
}

func publicUser(u *User) map[string]any {
	return map[string]any{
		"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin,
		"created_at": u.CreatedAt.Format(time.RFC3339),
	}
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("panel_session"); err == nil && c.Value != "" {
		a.store.Logout(c.Value)
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"user": publicUser(u), "version": panelVersion})
}

// ---------- rules ----------

type ruleDTO struct {
	Tag               string         `json:"tag"`
	ListenIP          string         `json:"listen_ip"`
	ListenPort        int            `json:"listen_port"`
	RemoteHost        string         `json:"remote_host"`
	RemoteTCPPort     int            `json:"remote_tcp_port"`
	RemoteUDPPort     int            `json:"remote_udp_port"`
	EnableTCP         bool           `json:"enable_tcp"`
	EnableUDP         bool           `json:"enable_udp"`
	Advanced          map[string]int `json:"advanced"`
	Remark            string         `json:"remark"`
	NodeID            int64          `json:"node_id"`
	RateLimitKBps     int            `json:"rate_limit_kbps"`     //实例限速 KB/s，0不限
	TrafficQuotaBytes int64          `json:"traffic_quota_bytes"` //流量配额字节，0不限
}

// validateRule 校验失败时直接写出错误响应并返回 nil。
func (a *API) validateRule(w http.ResponseWriter, req *ruleDTO, existingTag string) *Rule {
	rule, code, msg := a.checkRule(req, existingTag)
	if rule == nil {
		errJSON(w, code, msg)
	}
	return rule
}

// checkRule 校验并规范化规则；失败时返回 HTTP 状态码与可直接展示的错误信息。
func (a *API) checkRule(req *ruleDTO, existingTag string) (*Rule, int, string) {
	if !tagRe.MatchString(req.Tag) {
		return nil, 400, "标签只能包含字母数字下划线横杠，长度 1-32"
	}
	if req.ListenIP == "" {
		req.ListenIP = "0.0.0.0"
	}
	if !validLocalIP(req.ListenIP) {
		return nil, 400, "监听 IP 不合法（支持 IPv4/IPv6 地址）"
	}
	if !validPort(req.ListenPort) {
		return nil, 400, "监听端口需在 1-65535"
	}
	if net.ParseIP(req.RemoteHost) == nil && !remoteHostRe.MatchString(req.RemoteHost) {
		return nil, 400, "远端地址不合法（支持 IP 或域名）"
	}
	if !req.EnableTCP && !req.EnableUDP {
		return nil, 400, "TCP 和 UDP 至少启用一个"
	}
	if req.EnableTCP && !validPort(req.RemoteTCPPort) {
		return nil, 400, "远端 TCP 端口需在 1-65535"
	}
	if req.RemoteUDPPort == 0 && req.EnableTCP {
		req.RemoteUDPPort = req.RemoteTCPPort
	}
	if req.EnableUDP && !validPort(req.RemoteUDPPort) {
		return nil, 400, "远端 UDP 端口需在 1-65535"
	}
	if req.NodeID != 0 {
		n := a.store.GetNode(req.NodeID)
		if n == nil || n.Deleting {
			return nil, 400, "所选节点不存在或正在清理"
		}
	}
	if a.store.PortInUse(&Rule{NodeID: req.NodeID, ListenIP: req.ListenIP, ListenPort: req.ListenPort, EnableTCP: req.EnableTCP, EnableUDP: req.EnableUDP}, existingTag) {
		return nil, 409, fmt.Sprintf("该节点上监听 %s:%d 已被其他规则占用", req.ListenIP, req.ListenPort)
	}
	for k, v := range req.Advanced {
		spec, ok := advancedSpec[k]
		if !ok {
			return nil, 400, "未知高级参数: " + k
		}
		if v < spec[0] || v > spec[1] {
			return nil, 400, fmt.Sprintf("%s 取值范围 %d-%d", k, spec[0], spec[1])
		}
	}
	if req.RateLimitKBps < 0 || req.RateLimitKBps > 10000000 {
		return nil, 400, "限速取值范围 0-10000000 KB/s"
	}
	if req.TrafficQuotaBytes < 0 {
		return nil, 400, "流量配额不能为负数"
	}
	return &Rule{
		Tag: req.Tag, ListenIP: req.ListenIP, ListenPort: req.ListenPort,
		RemoteHost: req.RemoteHost, RemoteTCPPort: req.RemoteTCPPort,
		RemoteUDPPort: req.RemoteUDPPort, EnableTCP: req.EnableTCP,
		EnableUDP: req.EnableUDP, Advanced: req.Advanced, Remark: req.Remark,
		NodeID:            req.NodeID,
		RateLimitKBps:     req.RateLimitKBps,
		TrafficQuotaBytes: req.TrafficQuotaBytes,
	}, 200, ""
}

func (a *API) handleRulesList(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	rules := a.store.ListRules(u.ID, u.IsAdmin)
	type row struct {
		Rule
		Status   UnitStatus   `json:"status"`
		Stats    *RuleStats   `json:"stats"`
		Probe    *ProbeResult `json:"probe"`
		Owner    string       `json:"owner"`
		NodeName string       `json:"node_name"`
		MyRule   bool         `json:"my_rule"`
	}
	// 远端规则状态来自节点最近一次上报
	nodeReports := map[int64]*NodeReport{}
	for _, n := range a.store.ListNodes() {
		nodeReports[n.ID] = ParseNodeReport(n)
	}
	rows := make([]row, 0, len(rules))
	for _, rl := range rules {
		owner := a.store.GetUser(rl.OwnerID)
		st := UnitStatus{Active: "unknown", Enabled: "unknown"}
		nodeName := "本机"
		var stats *RuleStats
		if rl.NodeID != 0 {
			if n := a.store.GetNode(rl.NodeID); n != nil {
				nodeName = n.Name
				if rep, ok := nodeReports[n.ID]; ok {
					if s, ok2 := rep.Statuses[rl.Tag]; ok2 {
						st = s
					} else {
						st.Active = "pending" // 节点在线但尚未上报该实例
					}
					if !NodeOnline(n) {
						st.Active = "node-offline"
					}
					if rep.Stats != nil {
						if s, ok2 := rep.Stats[rl.Tag]; ok2 {
							stats = s
						}
					}
				}
			} else {
				nodeName = "(节点已删除)"
			}
		} else {
			st = a.sd.Status(rl.Tag)
			stats = ReadRuleStats(rl.Tag)
		}
		if stats == nil {
			stats = &RuleStats{}
		}
		if rl.OverQuota {
			stats.OverQuota = true
		}
		//定期测活：远端规则优先节点侧探测结果，本地规则用主控侧
		var probe *ProbeResult
		if rl.NodeID != 0 {
			if rep, ok := nodeReports[rl.NodeID]; ok && rep.Probes != nil {
				probe = rep.Probes[rl.Tag]
			}
		}
		if probe == nil && a.prober != nil {
			probe = a.prober.Get(rl.Tag)
		}
		rows = append(rows, row{
			Rule:     *rl,
			Status:   st,
			Stats:    stats,
			Probe:    probe,
			Owner:    ownerUsername(owner),
			NodeName: nodeName,
			MyRule:   rl.OwnerID == u.ID,
		})
	}
	writeJSON(w, 200, map[string]any{"rules": rows})
}

func ownerUsername(u *User) string {
	if u == nil {
		return "(已删除)"
	}
	return u.Username
}

func (a *API) handleRuleCreate(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	var req ruleDTO
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	if a.store.GetRule(req.Tag) != nil {
		errJSON(w, 409, "标签已存在: "+req.Tag)
		return
	}
	rule := a.validateRule(w, &req, "")
	if rule == nil {
		return
	}
	rule.OwnerID = u.ID
	if err := a.store.CreateRule(rule); err != nil {
		errJSON(w, 500, "保存失败: "+err.Error())
		return
	}
	if err := a.applyRule(rule, rule.NodeID); err != nil {
		errJSON(w, 500, "规则已保存但启动失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"rule": rule})
}

func (a *API) handleRuleUpdate(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	old := a.store.GetRule(tag)
	if old == nil {
		errJSON(w, 404, "规则不存在")
		return
	}
	if !u.IsAdmin && old.OwnerID != u.ID {
		errJSON(w, 403, "只能修改自己的转发")
		return
	}
	var req ruleDTO
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	if req.Tag != tag {
		errJSON(w, 400, "标签不可修改（删除后重建）")
		return
	}
	rule := a.validateRule(w, &req, tag)
	if rule == nil {
		return
	}
	rule.OwnerID = old.OwnerID
	rule.DesiredState = old.DesiredState
	// Stop the old local instance before publishing the remote assignment.
	if old.NodeID == 0 && rule.NodeID != 0 {
		if err := a.sd.DeleteUnit(rule.Tag); err != nil {
			errJSON(w, 500, "停止旧本机实例失败: "+err.Error())
			return
		}
	}
	if err := a.store.UpsertRule(rule); err != nil {
		if old.NodeID == 0 && rule.NodeID != 0 {
			a.restoreLocalRule(old)
		}
		errJSON(w, 500, "保存失败: "+err.Error())
		return
	}
	if err := a.applyRule(rule, old.NodeID); err != nil {
		errJSON(w, 500, "规则已保存但应用失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"rule": rule})
}

// applyRule 按节点归属应用规则：
//   - 本机规则：写 env + relays.conf 镜像并重启实例
//   - 远端规则：不落本机文件，由被控节点下次轮询（≤5s）拉取生效
//   - 本机 → 远端迁移：顺手停掉本机旧实例并清理 env
func (a *API) applyRule(rule *Rule, oldNodeID int64) error {
	rules := a.store.ListRules(0, true)
	if rule.NodeID != 0 {
		if oldNodeID == 0 {
			if err := a.sd.RemoveRuleFiles(rules, rule.Tag); err != nil {
				return err
			}
			a.sd.DaemonReload()
		}
		return nil
	}
	if err := a.sd.WriteRuleFiles(rules, rule); err != nil {
		return err
	}
	if rule.TrafficQuotaBytes > 0 {
		if stats := ReadRuleStats(rule.Tag); stats != nil && stats.Total() >= uint64(rule.TrafficQuotaBytes) {
			a.store.SetRuleOverQuota(rule.Tag, true)
			rule.OverQuota = true
		}
	}
	if rule.DesiredState == "stopped" || rule.OverQuota {
		return a.sd.StopRule(rule.Tag)
	}
	return a.sd.RestartRule(rule.Tag)
}

// deleteRule 删除一条规则：本机规则先停实例再删记录，删记录失败时按原期望状态
// 恢复实例，避免出现「规则还在、实例已停」。deleted 表示记录是否已删除，此时 err
// 只代表配置文件清理失败。daemon-reload 由调用方统一执行。
func (a *API) deleteRule(old *Rule) (deleted bool, err error) {
	if old.NodeID == 0 {
		if err := a.sd.DeleteUnit(old.Tag); err != nil {
			return false, fmt.Errorf("停止实例失败: %w", err)
		}
	}
	if _, err := a.store.DeleteRule(old.Tag); err != nil {
		if old.NodeID == 0 {
			a.restoreLocalRule(old)
		}
		return false, fmt.Errorf("删除失败: %w", err)
	}
	if old.NodeID == 0 {
		// 本机规则立即清理；远端规则由被控节点下次轮询发现后自行清理
		if err := a.sd.RemoveRuleFiles(a.store.ListRules(0, true), old.Tag); err != nil {
			return true, fmt.Errorf("规则已删除但配置文件清理失败: %w", err)
		}
	}
	return true, nil
}

// restoreLocalRule 在存储写入失败后按原配置恢复已被停掉的本机实例。
func (a *API) restoreLocalRule(old *Rule) {
	if err := a.applyRule(old, 0); err != nil {
		log.Printf("恢复本机实例 %s 失败: %v", old.Tag, err)
	}
}

func (a *API) handleRuleDelete(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	old := a.store.GetRule(tag)
	if old == nil {
		errJSON(w, 404, "规则不存在")
		return
	}
	if !u.IsAdmin && old.OwnerID != u.ID {
		errJSON(w, 403, "只能删除自己的转发")
		return
	}
	if _, err := a.deleteRule(old); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	if old.NodeID == 0 {
		a.sd.DaemonReload()
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

func (a *API) handleRuleAction(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	rule := a.store.GetRule(tag)
	if rule == nil {
		errJSON(w, 404, "规则不存在")
		return
	}
	if !u.IsAdmin && rule.OwnerID != u.ID {
		errJSON(w, 403, "只能操作自己的转发")
		return
	}
	desired := "running"
	if r.PathValue("action") == "stop" {
		desired = "stopped"
	} else if r.PathValue("action") != "start" {
		errJSON(w, 404, "未知操作")
		return
	}

	if rule.NodeID == 0 {
		// 本机规则：直接操作 systemd
		var err error
		if desired == "running" {
			err = a.sd.RestartRuleFresh(tag)
		} else {
			err = a.sd.StopRule(tag)
		}
		if err != nil {
			errJSON(w, 500, "操作失败: "+err.Error())
			return
		}
	}
	// 记录期望状态并推进版本；远端节点据此在下次轮询时执行
	if err := a.store.SetRuleDesiredState(tag, desired); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"desired": desired, "status": a.sd.Status(tag)})
}

func (a *API) handleRuleLogs(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	tag := r.PathValue("tag")
	rule := a.store.GetRule(tag)
	if rule == nil {
		errJSON(w, 404, "规则不存在")
		return
	}
	if !u.IsAdmin && rule.OwnerID != u.ID {
		errJSON(w, 403, "只能查看自己的转发日志")
		return
	}
	if rule.NodeID != 0 {
		errJSON(w, 400, "远端规则的日志在节点机器上，请登录该节点查看：journalctl -u tcp-pool@"+tag)
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			lines = n
		}
	}
	logs, err := a.sd.Logs(tag, lines)
	if err != nil {
		errJSON(w, 500, "读取日志失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"logs": logs})
}

// ---------- users (admin) ----------

func (a *API) handleUsersList(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	users := a.store.ListUsers()
	out := make([]map[string]any, 0, len(users))
	ruleCount := map[int64]int{}
	for _, rl := range a.store.ListRules(0, true) {
		ruleCount[rl.OwnerID]++
	}
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin,
			"disabled": u.Disabled, "created_at": u.CreatedAt.Format(time.RFC3339),
			"rule_count": ruleCount[u.ID],
		})
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

func (a *API) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	u, err := a.store.CreateUser(req.Username, req.Password, req.IsAdmin)
	if err == ErrDuplicate {
		errJSON(w, 409, "用户名已存在")
		return
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *API) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		errJSON(w, 400, "用户 ID 不合法")
		return
	}
	var req struct {
		Password *string `json:"password"`
		IsAdmin  *bool   `json:"is_admin"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	u, err := a.store.UpdateUser(id, req.Password, req.IsAdmin, req.Disabled)
	if err == ErrNotFound {
		errJSON(w, 404, "用户不存在")
		return
	}
	if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"user": publicUser(u)})
}

func (a *API) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	me, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		errJSON(w, 400, "用户 ID 不合法")
		return
	}
	if id == me.ID {
		errJSON(w, 400, "不能删除自己")
		return
	}
	if err := a.store.DeleteUserAndTransfer(id, me.ID); err == ErrNotFound {
		errJSON(w, 404, "用户不存在")
		return
	} else if err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// 修改自己的密码
func (a *API) handlePassword(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Old)) != nil {
		errJSON(w, 400, "原密码错误")
		return
	}
	if _, err := a.store.UpdateUser(u.ID, &req.New, nil, nil); err != nil {
		errJSON(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}

// ---------- system ----------

func (a *API) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{
		"tuning":  a.tuner.Status(),
		"machine": ReadMachineInfo(),
		"version": panelVersion,
	})
}

func (a *API) handleApplyBBR(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	if err := a.tuner.ApplyBBR(); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"tuning": a.tuner.Status()})
}

func (a *API) handleApplyTuning(w http.ResponseWriter, r *http.Request) {
	_, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var req struct {
		Profile string `json:"profile"`
		BBR     bool   `json:"bbr"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	if err := a.tuner.ApplyProfile(req.Profile, req.BBR); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"tuning": a.tuner.Status()})
}

// ---------- routes ----------

func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.handleMe)
	mux.HandleFunc("GET /api/overview", a.handleOverview)
	mux.HandleFunc("GET /api/rules", a.handleRulesList)
	mux.HandleFunc("POST /api/rules", a.handleRuleCreate)
	mux.HandleFunc("PUT /api/rules/{tag}", a.handleRuleUpdate)
	mux.HandleFunc("DELETE /api/rules/{tag}", a.handleRuleDelete)
	mux.HandleFunc("POST /api/rules/{tag}/{action}", a.handleRuleAction)
	mux.HandleFunc("GET /api/rules/{tag}/logs", a.handleRuleLogs)
	mux.HandleFunc("POST /api/rules/batch", a.handleRulesBatch)
	mux.HandleFunc("GET /api/rules/export", a.handleRulesExport)
	mux.HandleFunc("POST /api/rules/import", a.handleRulesImport)
	mux.HandleFunc("GET /api/users", a.handleUsersList)
	mux.HandleFunc("POST /api/users", a.handleUserCreate)
	mux.HandleFunc("PUT /api/users/{id}", a.handleUserUpdate)
	mux.HandleFunc("DELETE /api/users/{id}", a.handleUserDelete)
	mux.HandleFunc("POST /api/password", a.handlePassword)
	mux.HandleFunc("GET /api/system", a.handleSystemStatus)
	mux.HandleFunc("POST /api/system/bbr", a.handleApplyBBR)
	mux.HandleFunc("POST /api/system/tuning", a.handleApplyTuning)
	mux.HandleFunc("GET /api/system/update", a.handleUpdateStatus)
	mux.HandleFunc("POST /api/system/update", a.handleUpdateApply)
	mux.HandleFunc("GET /api/tls", a.handleTLSInfo)
	mux.HandleFunc("GET /api/traffic", a.handleTraffic)
	// 节点（主从）
	mux.HandleFunc("GET /api/nodes", a.handleNodesList)
	mux.HandleFunc("POST /api/nodes", a.handleNodeCreate)
	mux.HandleFunc("DELETE /api/nodes/{id}", a.handleNodeDelete)
	mux.HandleFunc("POST /api/nodes/{id}/key", a.handleNodeRegenKey)
	mux.HandleFunc("POST /api/nodes/upgrade", a.handleNodesUpgrade)
	// 被控机专用（Bearer 节点密钥）
	mux.HandleFunc("POST /api/node/pull", a.handleNodePull)
	mux.HandleFunc("POST /api/node/report", a.handleNodeReport)
}
