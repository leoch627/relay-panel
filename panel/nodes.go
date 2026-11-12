package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Node 是一台被控转发节点。被控机用 Key 认证，通过出站轮询拉取自己的规则集。
type Node struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Key        string    `json:"key"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeen   time.Time `json:"last_seen"`   // 最近一次心跳
	ReportJSON string    `json:"report_json"` // 最近一次上报原文
	Deleting   bool      `json:"deleting"`    //保留密钥直到节点确认已清理实例
}

func cloneNode(n *Node) *Node { cp := *n; return &cp }

// NodeReport 被控节点上报的内容，主控据此展示节点与远端规则状态。
type NodeReport struct {
	At            time.Time               `json:"at"`
	Machine       MachineInfo             `json:"machine"`
	TCPoolVersion string                  `json:"tcp_pool_version"`
	PanelVersion  string                  `json:"panel_version"`
	Statuses      map[string]UnitStatus   `json:"statuses"`
	Stats         map[string]*RuleStats   `json:"stats,omitempty"`
	Probes        map[string]*ProbeResult `json:"probes,omitempty"` //节点侧定期测活
	Error         string                  `json:"error,omitempty"`
}

// ---------- store：nodes ----------

func (s *Store) CreateNode(name string) (*Node, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 64 {
		return nil, fmt.Errorf("节点名长度需在 1-64 之间")
	}
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.st.Nodes {
		if strings.EqualFold(n.Name, name) {
			return nil, ErrDuplicate
		}
	}
	n := &Node{
		ID:        s.st.NextNodeID,
		Name:      name,
		Key:       hex.EncodeToString(buf),
		CreatedAt: time.Now(),
	}
	s.st.NextNodeID++
	s.st.Nodes = append(s.st.Nodes, n)
	return cloneNode(n), s.saveLocked()
}

func (s *Store) ListNodes() []*Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Node, len(s.st.Nodes))
	for i, n := range s.st.Nodes {
		out[i] = cloneNode(n)
	}
	return out
}

func (s *Store) GetNode(id int64) *Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.st.Nodes {
		if n.ID == id {
			return cloneNode(n)
		}
	}
	return nil
}

func (s *Store) GetNodeByKey(key string) *Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.st.Nodes {
		if n.Key == key {
			return cloneNode(n)
		}
	}
	return nil
}

// RegenerateNodeKey 重置密钥；旧密钥立即失效（被控机会持续 401 并保持现有实例）。
func (s *Store) RegenerateNodeKey(id int64) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	for _, n := range s.st.Nodes {
		if n.ID == id {
			if n.Deleting {
				return nil, fmt.Errorf("节点正在清理，不能重置密钥")
			}
			n.Key = hex.EncodeToString(buf)
			return cloneNode(n), s.saveLocked()
		}
	}
	return nil, ErrNotFound
}

// BeginDeleteNode retains authentication until cleanup is acknowledged.
func (s *Store) BeginDeleteNode(id int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var node *Node
	for _, n := range s.st.Nodes {
		if n.ID == id {
			node = n
			break
		}
	}
	if node == nil {
		return 0, ErrNotFound
	}
	oldRules, oldDeleting := s.st.Rules, node.Deleting
	kept := make([]*Rule, 0, len(oldRules))
	for _, r := range oldRules {
		if r.NodeID != id {
			kept = append(kept, r)
		}
	}
	s.st.Rules, node.Deleting = kept, true
	if err := s.saveLocked(); err != nil {
		s.st.Rules, node.Deleting = oldRules, oldDeleting
		return 0, err
	}
	return len(oldRules) - len(kept), nil
}

func (s *Store) CompleteNodeDeletion(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.st.Nodes
	nodes := make([]*Node, 0, len(old))
	found := false
	for _, n := range s.st.Nodes {
		if n.ID == id {
			if !n.Deleting {
				return fmt.Errorf("节点未请求删除")
			}
			found = true
		} else {
			nodes = append(nodes, n)
		}
	}
	if !found {
		return ErrNotFound
	}
	s.st.Nodes = nodes
	if err := s.saveLocked(); err != nil {
		s.st.Nodes = old
		return err
	}
	return nil
}

// TouchNode 更新心跳与上报原文；超过 30s 才落盘一次，避免高频写 JSON。
func (s *Store) TouchNode(id int64, reportJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.st.Nodes {
		if n.ID != id {
			continue
		}
		now := time.Now()
		shouldPersist := now.Sub(n.LastSeen) > 30*time.Second || n.ReportJSON == "" || n.ReportJSON != reportJSON
		n.LastSeen = now
		n.ReportJSON = reportJSON
		if shouldPersist {
			_ = s.saveLocked()
		}
		return
	}
}

// NodeOnline 判定节点在线：心跳在 20s 内（默认轮询 5s，容忍丢几轮）。
func NodeOnline(n *Node) bool {
	if n.LastSeen.IsZero() {
		return false
	}
	return time.Since(n.LastSeen) < 20*time.Second
}

// ParseNodeReport 解析持久化的上报原文；失败返回带 At 的空报告。
func ParseNodeReport(n *Node) *NodeReport {
	rep := &NodeReport{At: n.LastSeen, Statuses: map[string]UnitStatus{}}
	if n.ReportJSON == "" {
		return rep
	}
	var r NodeReport
	if err := json.Unmarshal([]byte(n.ReportJSON), &r); err == nil {
		if r.Statuses == nil {
			r.Statuses = map[string]UnitStatus{}
		}
		return &r
	}
	return rep
}
