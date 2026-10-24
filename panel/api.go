package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const panelVersion = "relay-panel v1.0.0 (tcp_pool v1.7)"

type API struct {
	store    *Store
	sd       *SystemdManager
	tuner    *Tuner
	run      *Runner
	sessions *SessionCache
	tls      *TLSConfig
	traffic  *TrafficCollector
}

func (a *API) tlsFingerprint() string {
	if a.tls == nil {
		return ""
	}
	return a.tls.Fingerprint
}

// SessionCache 进程内缓存 session -> user，避免每个请求都读磁盘。
type SessionCache struct {
	mu    sync.Mutex
	cache map[string]*User
}

func NewSessionCache() *SessionCache {
	return &SessionCache{cache: map[string]*User{}}
}

func (sc *SessionCache) Get(store *Store, token string) *User {
	sc.mu.Lock()
	u, ok := sc.cache[token]
	sc.mu.Unlock()
	if ok {
		// 缓存命中也回源校验一次（处理被禁用/删除/登出）
		if fresh := store.Auth(token); fresh != nil {
			return fresh
		}
		sc.mu.Lock()
		delete(sc.cache, token)
		sc.mu.Unlock()
		return nil
	}
	u = store.Auth(token)
	if u != nil {
		sc.mu.Lock()
		sc.cache[token] = u
		sc.mu.Unlock()
	}
	return u
}

func (sc *SessionCache) Drop(token string) {
	sc.mu.Lock()
	delete(sc.cache, token)
	sc.mu.Unlock()
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
	return a.sessions.Get(a.store, c.Value)
}

func (a *API) requireAuth(w http.ResponseWriter, r *http.Request) (*User, string, bool) {
	c, err := r.Cookie("panel_session")
	if err != nil || c.Value == "" {
		errJSON(w, http.StatusUnauthorized, "未登录")
		return nil, "", false
	}
	u := a.sessions.Get(a.store, c.Value)
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
	token, u, err := a.store.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, ErrBadAuth) {
			errJSON(w, 401, "用户名或密码错误")
		} else {
			//锁定/禁用等带具体原因的错误直接透传
			errJSON(w, 401, err.Error())
		}
		return
	}
	a.sessions.mu.Lock()
	a.sessions.cache[token] = u
	a.sessions.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "panel_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	writeJSON(w, 200, map[string]any{"user": publicUser(u)})
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
		a.sessions.Drop(c.Value)
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

func (a *API) validateRule(w http.ResponseWriter, req *ruleDTO, existingTag string) *Rule {
	if !tagRe.MatchString(req.Tag) {
		errJSON(w, 400, "标签只能包含字母数字下划线横杠，长度 1-32")
		return nil
	}
	if req.ListenIP == "" {
		req.ListenIP = "0.0.0.0"
	}
	if !validLocalIP(req.ListenIP) {
		errJSON(w, 400, "监听 IP 不合法（支持 IPv4/IPv6 地址）")
		return nil
	}
	if !validPort(req.ListenPort) {
		errJSON(w, 400, "监听端口需在 1-65535")
		return nil
	}
	if !remoteHostRe.MatchString(req.RemoteHost) {
		errJSON(w, 400, "远端地址不合法（支持 IP 或域名）")
		return nil
	}
	if !req.EnableTCP && !req.EnableUDP {
		errJSON(w, 400, "TCP 和 UDP 至少启用一个")
		return nil
	}
	if req.EnableTCP && !validPort(req.RemoteTCPPort) {
		errJSON(w, 400, "远端 TCP 端口需在 1-65535")
		return nil
	}
	if req.RemoteUDPPort == 0 && req.EnableTCP {
		req.RemoteUDPPort = req.RemoteTCPPort
	}
	if req.EnableUDP && !validPort(req.RemoteUDPPort) {
		errJSON(w, 400, "远端 UDP 端口需在 1-65535")
		return nil
	}
	if req.NodeID != 0 && a.store.GetNode(req.NodeID) == nil {
		errJSON(w, 400, "所选节点不存在")
		return nil
	}
	if a.store.PortInUse(req.NodeID, req.ListenIP, req.ListenPort, existingTag) {
		errJSON(w, 409, fmt.Sprintf("该节点上监听 %s:%d 已被其他规则占用", req.ListenIP, req.ListenPort))
		return nil
	}
	for k, v := range req.Advanced {
		spec, ok := advancedSpec[k]
		if !ok {
			errJSON(w, 400, "未知高级参数: "+k)
			return nil
		}
		if v < spec[0] || v > spec[1] {
			errJSON(w, 400, fmt.Sprintf("%s 取值范围 %d-%d", k, spec[0], spec[1]))
			return nil
		}
	}
	if req.RateLimitKBps < 0 || req.RateLimitKBps > 10000000 {
		errJSON(w, 400, "限速取值范围 0-10000000 KB/s")
		return nil
	}
	if req.TrafficQuotaBytes < 0 {
		errJSON(w, 400, "流量配额不能为负数")
		return nil
	}
	return &Rule{
		Tag: req.Tag, ListenIP: req.ListenIP, ListenPort: req.ListenPort,
		RemoteHost: req.RemoteHost, RemoteTCPPort: req.RemoteTCPPort,
		RemoteUDPPort: req.RemoteUDPPort, EnableTCP: req.EnableTCP,
		EnableUDP: req.EnableUDP, Advanced: req.Advanced, Remark: req.Remark,
		NodeID:            req.NodeID,
		RateLimitKBps:     req.RateLimitKBps,
		TrafficQuotaBytes: req.TrafficQuotaBytes,
	}
}

func (a *API) handleRulesList(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAuth(w, r)
	if !ok {
		return
	}
	rules := a.store.ListRules(u.ID, u.IsAdmin)
	type row struct {
		Rule
		Status   UnitStatus `json:"status"`
		Stats    *RuleStats `json:"stats"`
		Owner    string     `json:"owner"`
		NodeName string     `json:"node_name"`
		MyRule   bool       `json:"my_rule"`
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
		rows = append(rows, row{
			Rule:     *rl,
			Status:   st,
			Stats:    stats,
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
	if err := a.store.UpsertRule(rule); err != nil {
		errJSON(w, 500, "保存失败: "+err.Error())
		return
	}
	if err := a.applyRule(rule, 0); err != nil {
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
	if err := a.store.UpsertRule(rule); err != nil {
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
			_ = a.sd.DeleteUnit(rule.Tag)
			_ = a.sd.RemoveRuleFiles(rules, rule.Tag)
			a.sd.DaemonReload()
		}
		return nil
	}
	if err := a.sd.WriteRuleFiles(rules, rule); err != nil {
		return err
	}
	if oldNodeID != 0 {
		// 远端迁回本机：正常重启即可
		return a.sd.RestartRule(rule.Tag)
	}
	return a.sd.RestartRule(rule.Tag)
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
	if _, err := a.store.DeleteRule(tag); err != nil {
		errJSON(w, 500, "删除失败: "+err.Error())
		return
	}
	if old.NodeID == 0 {
		// 本机规则立即清理；远端规则由被控节点下次轮询发现后自行清理
		_ = a.sd.DeleteUnit(tag)
		rules := a.store.ListRules(0, true)
		if err := a.sd.RemoveRuleFiles(rules, tag); err != nil {
			errJSON(w, 500, "规则已删除但配置文件清理失败: "+err.Error())
			return
		}
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
			err = a.sd.RestartRule(tag)
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
	// 该用户的规则移交给操作者，避免变成孤儿实例
	for _, rl := range a.store.ListRules(id, true) {
		rl.OwnerID = me.ID
		_ = a.store.UpsertRule(rl)
	}
	if err := a.store.DeleteUser(id); err == ErrNotFound {
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
	_, _, ok := a.requireAuth(w, r)
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
	mux.HandleFunc("GET /api/tls", a.handleTLSInfo)
	mux.HandleFunc("GET /api/traffic", a.handleTraffic)
	// 节点（主从）
	mux.HandleFunc("GET /api/nodes", a.handleNodesList)
	mux.HandleFunc("POST /api/nodes", a.handleNodeCreate)
	mux.HandleFunc("DELETE /api/nodes/{id}", a.handleNodeDelete)
	mux.HandleFunc("POST /api/nodes/{id}/key", a.handleNodeRegenKey)
	// 被控机专用（Bearer 节点密钥）
	mux.HandleFunc("POST /api/node/pull", a.handleNodePull)
	mux.HandleFunc("POST /api/node/report", a.handleNodeReport)
}
