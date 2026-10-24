package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// IsLoginLocked 同一用户名连续失败 5 次锁定 10 分钟（内存级，重启即清）。
func (s *Store) IsLoginLocked(username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.loginFails[username]
	if !ok {
		return false
	}
	return time.Now().Before(a.LockUntil)
}

func (s *Store) RecordLoginFailure(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.loginFails[username]
	a.Fails++
	if a.Fails >= 5 {
		a.LockUntil = time.Now().Add(10 * time.Minute)
		a.Fails = 0
	}
	s.loginFails[username] = a
}

func (s *Store) RecordLoginSuccess(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.loginFails, username)
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(&s.st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0o600)
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
	return u, s.saveLocked()
}

func (s *Store) ListUsers() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*User, len(s.st.Users))
	copy(out, s.st.Users)
	return out
}

func (s *Store) GetUser(id int64) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.st.Users {
		if u.ID == id {
			return u
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
	if password != nil {
		if len(*password) < 6 {
			return nil, fmt.Errorf("密码至少 6 位")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		u.PasswordHash = string(hash)
		// 改密码后踢掉该用户所有会话
		for tok, sess := range s.st.Sessions {
			if sess.UserID == id {
				delete(s.st.Sessions, tok)
			}
		}
	}
	if isAdmin != nil {
		u.IsAdmin = *isAdmin
	}
	if disabled != nil {
		// 不允许禁用最后一个可用管理员
		if *disabled && u.IsAdmin {
			active := 0
			for _, x := range s.st.Users {
				if x.IsAdmin && !x.Disabled && x.ID != id {
					active++
				}
			}
			if active == 0 {
				return nil, fmt.Errorf("不能禁用最后一个管理员")
			}
		}
		u.Disabled = *disabled
	}
	return u, s.saveLocked()
}

func (s *Store) DeleteUser(id int64) error {
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
	users := s.st.Users[:0]
	for _, x := range s.st.Users {
		if x.ID != id {
			users = append(users, x)
		}
	}
	s.st.Users = users
	for tok, sess := range s.st.Sessions {
		if sess.UserID == id {
			delete(s.st.Sessions, tok)
		}
	}
	return s.saveLocked()
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

func (s *Store) Login(username, password string) (string, *User, error) {
	username = strings.TrimSpace(username)
	if s.IsLoginLocked(username) {
		return "", nil, fmt.Errorf("失败次数过多，账号已临时锁定 10 分钟")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.st.Users {
		if strings.EqualFold(u.Username, username) {
			if u.Disabled {
				return "", nil, fmt.Errorf("账号已被禁用")
			}
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
				s.mu.Unlock()
				s.RecordLoginFailure(username)
				s.mu.Lock()
				return "", nil, ErrBadAuth
			}
			s.mu.Unlock()
			s.RecordLoginSuccess(username)
			s.mu.Lock()
			tok := make([]byte, 32)
			if _, err := rand.Read(tok); err != nil {
				return "", nil, err
			}
			token := hex.EncodeToString(tok)
			s.st.Sessions[token] = Session{UserID: u.ID, ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
			return token, u, s.saveLocked()
		}
	}
	return "", nil, ErrBadAuth
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
			return u
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
			out = append(out, r)
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
			return r
		}
	}
	return nil
}

// UpsertRule 新增或更新规则；冲突返回 ErrDuplicate。
func (s *Store) UpsertRule(r *Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if r.DesiredState == "" {
		r.DesiredState = "running"
	}
	for _, x := range s.st.Rules {
		if x.Tag == r.Tag {
			r.CreatedAt = x.CreatedAt
			x.ListenIP = r.ListenIP
			x.ListenPort = r.ListenPort
			x.RemoteHost = r.RemoteHost
			x.RemoteTCPPort = r.RemoteTCPPort
			x.RemoteUDPPort = r.RemoteUDPPort
			x.EnableTCP = r.EnableTCP
			x.EnableUDP = r.EnableUDP
			x.Advanced = r.Advanced
			x.OwnerID = r.OwnerID
			x.Remark = r.Remark
			x.NodeID = r.NodeID
			x.DesiredState = r.DesiredState
			x.RateLimitKBps = r.RateLimitKBps
			x.TrafficQuotaBytes = r.TrafficQuotaBytes
			x.OverQuota = false //任何编辑都视为处理过超限状态
			x.UpdatedAt = now
			return s.saveLocked()
		}
	}
	r.CreatedAt = now
	r.UpdatedAt = now
	s.st.Rules = append(s.st.Rules, r)
	return s.saveLocked()
}

// SetRuleDesiredState 设置期望状态并推进版本（被控节点按版本变化感知重启）。
func (s *Store) SetRuleDesiredState(tag, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == tag {
			x.DesiredState = state
			x.UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return ErrNotFound
}

func (s *Store) DeleteRule(tag string) (*Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.st.Rules {
		if x.Tag == tag {
			s.st.Rules = append(s.st.Rules[:i], s.st.Rules[i+1:]...)
			return x, s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// PortInUse 检查同一节点上的监听冲突（不同节点/本机之间互不影响）。
func (s *Store) PortInUse(nodeID int64, listenIP string, port int, ignoreTag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.st.Rules {
		if x.Tag == ignoreTag || x.NodeID != nodeID {
			continue
		}
		sameIP := x.ListenIP == listenIP ||
			(isWildcardIP(x.ListenIP) && isWildcardIP(listenIP))
		if sameIP && x.ListenPort == port {
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
			out = append(out, x)
		}
	}
	return out
}

// DeleteRulesByNode 级联删除节点名下规则（本函数不触发 systemd 清理，由调用方负责）。
func (s *Store) DeleteRulesByNode(nodeID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.st.Rules[:0]
	n := 0
	for _, x := range s.st.Rules {
		if x.NodeID == nodeID {
			n++
			continue
		}
		kept = append(kept, x)
	}
	s.st.Rules = kept
	if n > 0 {
		_ = s.saveLocked()
	}
	return n
}
