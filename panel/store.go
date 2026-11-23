package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
	ErrBadAuth   = errors.New("bad credentials")
)

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	IsAdmin      bool      `json:"is_admin"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// Rule 是面板里的一条转发规则，tag 即 systemd 实例名 tcp-pool@<tag>。
// NodeID==0 表示跑在主控本机；否则跑在对应被控节点上。
type Rule struct {
	Tag               string         `json:"tag"`
	ListenIP          string         `json:"listen_ip"`
	ListenPort        int            `json:"listen_port"`
	RemoteHost        string         `json:"remote_host"`
	RemoteTCPPort     int            `json:"remote_tcp_port"`
	RemoteUDPPort     int            `json:"remote_udp_port"`
	EnableTCP         bool           `json:"enable_tcp"`
	EnableUDP         bool           `json:"enable_udp"`
	Advanced          map[string]int `json:"advanced,omitempty"`
	OwnerID           int64          `json:"owner_id"`
	Remark            string         `json:"remark"`
	NodeID            int64          `json:"node_id"`
	DesiredState      string         `json:"desired_state"`       // running | stopped
	RateLimitKBps     int            `json:"rate_limit_kbps"`     //实例限速 KB/s，0不限
	TrafficQuotaBytes int64          `json:"traffic_quota_bytes"` //流量配额字节，0不限
	OverQuota         bool           `json:"over_quota"`          //已超配额被自动停机
	QuotaReset        int64          `json:"quota_reset"`         //显式启动时重置配额停机状态
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type Session struct {
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type state struct {
	Users      []*User            `json:"users"`
	NextUserID int64              `json:"next_user_id"`
	NextNodeID int64              `json:"next_node_id"`
	Nodes      []*Node            `json:"nodes"`
	Rules      []*Rule            `json:"rules"`
	Sessions   map[string]Session `json:"sessions"`
}

type Store struct {
	mu         sync.Mutex
	path       string
	st         state
	loginFails map[string]loginAttempt
}

type loginAttempt struct {
	Fails     int
	First     time.Time // 当前计数窗口的起点
	LockUntil time.Time
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, loginFails: map[string]loginAttempt{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.st = state{NextUserID: 1, NextNodeID: 1, Sessions: map[string]Session{}}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.st); err != nil {
		return nil, fmt.Errorf("解析状态文件 %s 失败: %w", path, err)
	}
	if s.st.Sessions == nil {
		s.st.Sessions = map[string]Session{}
	}
	if s.st.NextUserID == 0 {
		s.st.NextUserID = 1
	}
	if s.st.NextNodeID == 0 {
		s.st.NextNodeID = 1
	}
	return s, nil
}

// ---------- 登录防爆破 ----------
//
// 失败计数按「客户端地址 + 用户名」与「客户端地址」两个维度记录（内存级，重启即清）：
//   - 同一地址对同一用户名 10 分钟内失败 5 次，锁定该组合 10 分钟；
//   - 同一地址 10 分钟内累计失败 20 次（换用户名喷洒），锁定该地址 10 分钟。
// 攻击者从自己的地址无法把管理员锁在门外；不存在的用户名同样计数，
// 锁定与否不暴露账号是否存在。

const (
	loginWindow      = 10 * time.Minute
	loginPairLimit   = 5
	loginClientLimit = 20
	loginTableMax    = 4096 // 超过后顺手清理过期记录，防止随机用户名撑爆内存
)

func loginPairKey(clientIP, username string) string { return "u\x00" + clientIP + "\x00" + username }
func loginClientKey(clientIP string) string         { return "ip\x00" + clientIP }

func (s *Store) loginLockedLocked(key string, now time.Time) bool {
	a, ok := s.loginFails[key]
	return ok && now.Before(a.LockUntil)
}

func (s *Store) recordLoginFailureLocked(key string, limit int, now time.Time) {
	a := s.loginFails[key]
	if a.First.IsZero() || now.Sub(a.First) > loginWindow {
		a = loginAttempt{First: now, LockUntil: a.LockUntil}
	}
	a.Fails++
	if a.Fails >= limit {
		a.LockUntil = now.Add(loginWindow)
		a.Fails = 0
		a.First = time.Time{}
	}
	s.loginFails[key] = a
}

func (s *Store) pruneLoginFailsLocked(now time.Time) {
	if len(s.loginFails) <= loginTableMax {
		return
	}
	for k, a := range s.loginFails {
		if !now.Before(a.LockUntil) && now.Sub(a.First) > loginWindow {
			delete(s.loginFails, k)
		}
	}
}

// IsLoginLocked 报告该地址对该用户名的登录是否处于锁定期。
func (s *Store) IsLoginLocked(clientIP, username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	return s.loginLockedLocked(loginClientKey(clientIP), now) || s.loginLockedLocked(loginPairKey(clientIP, username), now)
}

func (s *Store) RecordLoginFailure(clientIP, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneLoginFailsLocked(now)
	s.recordLoginFailureLocked(loginPairKey(clientIP, username), loginPairLimit, now)
	s.recordLoginFailureLocked(loginClientKey(clientIP), loginClientLimit, now)
}

// RecordLoginSuccess 只清除该用户名的计数；地址维度保留，避免用一个已知账号反复登录来洗掉喷洒计数。
func (s *Store) RecordLoginSuccess(clientIP, username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loginFails, loginPairKey(clientIP, username))
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

// 用户名不存在时也做一次同等代价的 bcrypt 比较，避免靠响应时间枚举用户名。
func loginDummyHash() []byte {
	dummyHashOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("relay-panel-dummy-password"), bcrypt.DefaultCost)
	})
	return dummyHash
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(&s.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0o600)
}

// Readers receive snapshots: callers must not mutate state after releasing mu.
func cloneUser(u *User) *User { cp := *u; return &cp }
func cloneRule(r *Rule) *Rule {
	cp := *r
	cp.Advanced = maps.Clone(r.Advanced)
	return &cp
}

// ---------- users ----------

func (s *Store) CreateUser(username, password string, isAdmin bool) (*User, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 || len(username) > 32 {
		return nil, fmt.Errorf("用户名长度需在 2-32 之间")
	}
	if len(password) < 6 {
		return nil, fmt.Errorf("密码至少 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.st.Users {
		if strings.EqualFold(u.Username, username) {
			return nil, ErrDuplicate
		}
	}
	u := &User{
		ID:           s.st.NextUserID,
		Username:     username,
		PasswordHash: string(hash),
		IsAdmin:      isAdmin,
		CreatedAt:    time.Now(),
	}
	s.st.NextUserID++
	s.st.Users = append(s.st.Users, u)
	return cloneUser(u), s.saveLocked()
}

func (s *Store) ListUsers() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*User, len(s.st.Users))
	for i, u := range s.st.Users {
		out[i] = cloneUser(u)
	}
	return out
}

func (s *Store) GetUser(id int64) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.st.Users {
		if u.ID == id {
			return cloneUser(u)
		}
	}
	return nil
}

func (s *Store) CountAdmins() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, u := range s.st.Users {
		if u.IsAdmin && !u.Disabled {
			n++
		}
	}
	return n
}

func (s *Store) UpdateUser(id int64, password *string, isAdmin *bool, disabled *bool) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u *User
	for _, x := range s.st.Users {
		if x.ID == id {
			u = x
			break
		}
	}
	if u == nil {
		return nil, ErrNotFound
	}
	next := *u
	if isAdmin != nil {
		next.IsAdmin = *isAdmin
	}
	if disabled != nil {
		next.Disabled = *disabled
	}
	if u.IsAdmin && !u.Disabled && (!next.IsAdmin || next.Disabled) {
		otherAdmin := false
		for _, x := range s.st.Users {
			if x.ID != id && x.IsAdmin && !x.Disabled {
				otherAdmin = true
				break
			}
		}
		if !otherAdmin {
			return nil, fmt.Errorf("不能禁用或降级最后一个管理员")
		}
	}
	oldSessions := maps.Clone(s.st.Sessions)
	if password != nil {
		if len(*password) < 6 {
			return nil, fmt.Errorf("密码至少 6 位")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		next.PasswordHash = string(hash)
		// 改密码后踢掉该用户所有会话
		for tok, sess := range s.st.Sessions {
			if sess.UserID == id {
				delete(s.st.Sessions, tok)
			}
		}
	}
	old := *u
	*u = next
	if err := s.saveLocked(); err != nil {
		*u = old
		s.st.Sessions = oldSessions
		return nil, err
	}
	return cloneUser(u), nil
}

// DeleteUserAndTransfer commits deletion and ownership changes together.
func (s *Store) DeleteUserAndTransfer(id, recipientID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u *User
	for _, x := range s.st.Users {
		if x.ID == id {
			u = x
			break
		}
	}
	if u == nil {
		return ErrNotFound
	}
	recipient := false
	for _, x := range s.st.Users {
		if x.ID == recipientID && x.ID != id {
			recipient = true
		}
	}
	if !recipient {
		return fmt.Errorf("规则接收用户不存在")
	}
	if u.IsAdmin {
		active := 0
		for _, x := range s.st.Users {
			if x.IsAdmin && !x.Disabled && x.ID != id {
				active++
			}
		}
		if active == 0 {
			return fmt.Errorf("不能删除最后一个管理员")
		}
	}
	old := s.st
	users := make([]*User, 0, len(s.st.Users)-1)
	for _, x := range s.st.Users {
		if x.ID != id {
			users = append(users, x)
		}
	}
	s.st.Users = users
	s.st.Rules = make([]*Rule, len(old.Rules))
	for i, r := range old.Rules {
		s.st.Rules[i] = cloneRule(r)
		if r.OwnerID == id {
			s.st.Rules[i].OwnerID = recipientID
		}
	}
	s.st.Sessions = maps.Clone(old.Sessions)
	for tok, sess := range s.st.Sessions {
		if sess.UserID == id {
			delete(s.st.Sessions, tok)
		}
	}
	if err := s.saveLocked(); err != nil {
		s.st = old
		return err
	}
	return nil
}

// ---------- sessions ----------

// SetRuleOverQuota 标记/清除超限状态（同值不落盘，避免无谓写放大）。
func (s *Store) SetRuleOverQuota(tag string, over bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == tag {
			if x.OverQuota == over {
				return
			}
			x.OverQuota = over
			_ = s.saveLocked()
			return
		}
	}
}

var ErrLoginLocked = errors.New("尝试次数过多，请 10 分钟后再试")

// Login 校验用户名密码并创建会话。clientIP 用于防爆破计数。
// bcrypt 比较在锁外进行，爆破请求不会阻塞节点轮询等其它操作。
func (s *Store) Login(username, password, clientIP string) (string, *User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if s.IsLoginLocked(clientIP, username) {
		return "", nil, ErrLoginLocked
	}
	var u *User
	s.mu.Lock()
	for _, x := range s.st.Users {
		if strings.EqualFold(x.Username, username) {
			u = cloneUser(x)
			break
		}
	}
	s.mu.Unlock()

	hash := loginDummyHash()
	if u != nil {
		hash = []byte(u.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || u == nil {
		s.RecordLoginFailure(clientIP, username)
		return "", nil, ErrBadAuth
	}
	// 密码正确后才提示禁用，未持有密码的人无法借此判断账号状态
	if u.Disabled {
		return "", nil, fmt.Errorf("账号已被禁用")
	}
	s.RecordLoginSuccess(clientIP, username)

	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return "", nil, err
	}
	token := hex.EncodeToString(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// 过期会话只在被再次使用时才会删除，顺带在每次登录时清理，避免状态文件无限增长
	oldSessions := maps.Clone(s.st.Sessions)
	for tok, sess := range s.st.Sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.st.Sessions, tok)
		}
	}
	s.st.Sessions[token] = Session{UserID: u.ID, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err := s.saveLocked(); err != nil {
		s.st.Sessions = oldSessions
		return "", nil, err
	}
	return token, u, nil
}

func (s *Store) Auth(token string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.st.Sessions[token]
	if !ok {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(s.st.Sessions, token)
		return nil
	}
	for _, u := range s.st.Users {
		if u.ID == sess.UserID {
			if u.Disabled {
				return nil
			}
			return cloneUser(u)
		}
	}
	return nil
}

func (s *Store) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.st.Sessions, token)
	_ = s.saveLocked()
}

// ---------- rules ----------

func (s *Store) ListRules(ownerID int64, isAdmin bool) []*Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Rule
	for _, r := range s.st.Rules {
		if isAdmin || r.OwnerID == ownerID {
			out = append(out, cloneRule(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

func (s *Store) GetRule(tag string) *Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.st.Rules {
		if r.Tag == tag {
			return cloneRule(r)
		}
	}
	return nil
}

// UpsertRule 新增或更新规则；冲突返回 ErrDuplicate。
func (s *Store) UpsertRule(r *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.upsertRuleLocked(r)
}

func (s *Store) CreateRule(r *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == r.Tag {
			return ErrDuplicate
		}
	}
	return s.upsertRuleLocked(r)
}

func (s *Store) upsertRuleLocked(r *Rule) error {
	if r.NodeID != 0 {
		available := false
		for _, n := range s.st.Nodes {
			if n.ID == r.NodeID && !n.Deleting {
				available = true
				break
			}
		}
		if !available {
			return fmt.Errorf("所选节点不存在或正在清理")
		}
	}
	for _, x := range s.st.Rules {
		if x.Tag != r.Tag && rulesConflict(x, r) {
			return fmt.Errorf("监听地址和协议已被规则 %s 占用", x.Tag)
		}
	}
	now := time.Now()
	if r.DesiredState == "" {
		r.DesiredState = "running"
	}
	for i, x := range s.st.Rules {
		if x.Tag == r.Tag {
			r.CreatedAt = x.CreatedAt
			r.QuotaReset = x.QuotaReset
			r.OverQuota = x.OverQuota && r.TrafficQuotaBytes > 0
			if r.TrafficQuotaBytes > x.TrafficQuotaBytes {
				r.OverQuota = false
			}
			if !now.After(x.UpdatedAt) || now.UnixMilli() <= x.UpdatedAt.UnixMilli() {
				now = x.UpdatedAt.Add(time.Millisecond)
			}
			r.UpdatedAt = now
			s.st.Rules[i] = cloneRule(r)
			if err := s.saveLocked(); err != nil {
				s.st.Rules[i] = x
				return err
			}
			return nil
		}
	}
	r.CreatedAt = now
	r.UpdatedAt = now
	s.st.Rules = append(s.st.Rules, cloneRule(r))
	if err := s.saveLocked(); err != nil {
		s.st.Rules = s.st.Rules[:len(s.st.Rules)-1]
		return err
	}
	return nil
}

// SetRuleDesiredState 设置期望状态并推进版本（被控节点按版本变化感知重启）。
func (s *Store) SetRuleDesiredState(tag, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == tag {
			old := *x
			x.DesiredState = state
			now := time.Now()
			if now.UnixMilli() <= x.UpdatedAt.UnixMilli() {
				now = x.UpdatedAt.Add(time.Millisecond)
			}
			x.UpdatedAt = now
			if state == "running" {
				x.QuotaReset = now.UnixMilli()
				x.OverQuota = false
			}
			if err := s.saveLocked(); err != nil {
				*x = old
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}

func (s *Store) DeleteRule(tag string) (*Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.st.Rules {
		if x.Tag == tag {
			old := s.st.Rules
			s.st.Rules = append(append([]*Rule{}, old[:i]...), old[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.st.Rules = old
				return nil, err
			}
			return cloneRule(x), nil
		}
	}
	return nil, ErrNotFound
}

// PortInUse 检查同一节点上的监听冲突（不同节点/本机之间互不影响）。
func rulesConflict(a, b *Rule) bool {
	if a.NodeID != b.NodeID || a.ListenPort != b.ListenPort {
		return false
	}
	if !(a.EnableTCP && b.EnableTCP || a.EnableUDP && b.EnableUDP) {
		return false
	}
	return a.ListenIP == b.ListenIP || a.ListenIP == "::" || b.ListenIP == "::" ||
		(!isIPv6(a.ListenIP) && !isIPv6(b.ListenIP) && (isWildcardIP(a.ListenIP) || isWildcardIP(b.ListenIP)))
}

func (s *Store) PortInUse(rule *Rule, ignoreTag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == ignoreTag {
			continue
		}
		if rulesConflict(x, rule) {
			return true
		}
	}
	return false
}

// RulesOnNode 返回某节点名下的全部规则（删除节点时级联）。
func (s *Store) RulesOnNode(nodeID int64) []*Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Rule
	for _, x := range s.st.Rules {
		if x.NodeID == nodeID {
			out = append(out, cloneRule(x))
		}
	}
	return out
}
