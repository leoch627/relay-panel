package main

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// ProbeResult 规则远端探测结果（TCP 建连时延）。
type ProbeResult struct {
	Ok  bool   `json:"ok"`
	Ms  int64  `json:"ms"`
	At  string `json:"at"`
	Err string `json:"err,omitempty"`
	// UDP-only 规则探测失败时标记 soft，前端弱化显示（无响应≠不可用）
	Soft bool `json:"soft,omitempty"`
}

// Prober 主控侧定期测活：对每条规则 TCP 建连其远端端口并测时延。
// 远端规则另有节点侧探测（更贴近真实路径），主控值作兜底展示。
type Prober struct {
	mu      sync.Mutex
	results map[string]*ProbeResult
	store   *Store
}

func NewProber(store *Store) *Prober {
	return &Prober{results: map[string]*ProbeResult{}, store: store}
}

// probeOne 探测单个地址，返回结果。
func probeOne(host string, port int, soft bool) *ProbeResult {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	lat := time.Since(start).Milliseconds()
	res := &ProbeResult{At: time.Now().Format(time.RFC3339), Soft: soft}
	if err != nil {
		res.Err = err.Error()
		if len(res.Err) > 80 {
			res.Err = res.Err[:80]
		}
		return res
	}
	conn.Close()
	res.Ok = true
	res.Ms = lat
	return res
}

func ruleProbeTarget(r *Rule) (host string, port int, soft bool, ok bool) {
	if r.EnableTCP && validPort(r.RemoteTCPPort) {
		return r.RemoteHost, r.RemoteTCPPort, false, true
	}
	if r.EnableUDP && validPort(r.RemoteUDPPort) {
		//UDP-only：探测同端口 TCP，失败只弱提示
		return r.RemoteHost, r.RemoteUDPPort, true, true
	}
	return "", 0, false, false
}

func (p *Prober) Cycle() {
	for _, r := range p.store.ListRules(0, true) {
		host, port, soft, ok := ruleProbeTarget(r)
		if !ok {
			continue
		}
		res := probeOne(host, port, soft)
		p.mu.Lock()
		p.results[r.Tag] = res
		p.mu.Unlock()
	}
}

func (p *Prober) Run(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		p.Cycle()
	}
}

// Get 主控侧探测结果。
func (p *Prober) Get(tag string) *ProbeResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.results[tag]
	if r == nil {
		return nil
	}
	cp := *r
	return &cp
}

// StartProber 主控启动定期测活（dry-run 跳过）。
func StartProber(store *Store, dryRun bool, interval time.Duration) *Prober {
	pr := NewProber(store)
	if dryRun {
		return pr
	}
	go pr.Run(interval)
	return pr
}
