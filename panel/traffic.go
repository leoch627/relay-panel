package main

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// TrafficCollector 周期采样各规则累计计数（本地 stats 文件 + 节点上报），
// 差量落进 5 分钟桶与日桶，供流量统计页查询。参照 cc-switch 用量页：
// 趋势图 + 年度热力图 + 分组统计。
type TrafficCollector struct {
	mu       sync.Mutex
	store    *Store
	path     string
	interval time.Duration

	// db 持久化结构
	db *trafficDB
}

type bucketVal struct {
	Up    uint64 `json:"up"`
	Down  uint64 `json:"down"`
	Conns uint64 `json:"conns"`
}

type trafficDB struct {
	Updated time.Time                        `json:"updated"`
	Last    map[string]bucketVal             `json:"last"`    //每规则最近一次的累计值（含重置检测）
	Buckets map[string]map[int64]*bucketVal  `json:"buckets"` //规则 -> 5分钟桶起点unix -> 差量
	Days    map[string]map[string]*bucketVal `json:"days"`    //规则 -> "2006-01-02" -> 差量
}

const (
	trafficBucketSec int64 = 300 //5分钟
	trafficRawKeep         = 48 * time.Hour
	trafficDaysKeep        = 400 * 24 * time.Hour
)

func LoadTrafficCollector(store *Store, path string, interval time.Duration) *TrafficCollector {
	tc := &TrafficCollector{store: store, path: path, interval: interval}
	tc.db = &trafficDB{
		Last:    map[string]bucketVal{},
		Buckets: map[string]map[int64]*bucketVal{},
		Days:    map[string]map[string]*bucketVal{},
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var db trafficDB
		if json.Unmarshal(data, &db) == nil {
			if db.Buckets == nil {
				db.Buckets = map[string]map[int64]*bucketVal{}
			}
			if db.Days == nil {
				db.Days = map[string]map[string]*bucketVal{}
			}
			if db.Last == nil {
				db.Last = map[string]bucketVal{}
			}
			tc.db = &db
		}
	}
	return tc
}

func (tc *TrafficCollector) saveLocked() {
	tc.db.Updated = time.Now()
	data, err := json.Marshal(tc.db)
	if err != nil {
		return
	}
	_ = writeFileAtomic(tc.path, data, 0o600)
}

// currentTotals 汇总此刻每规则的累计计数（本地文件 + 节点最近上报）。
func (tc *TrafficCollector) currentTotals() map[string]*RuleStats {
	out := map[string]*RuleStats{}
	nodeReports := map[int64]*NodeReport{}
	for _, n := range tc.store.ListNodes() {
		nodeReports[n.ID] = ParseNodeReport(n)
	}
	for _, r := range tc.store.ListRules(0, true) {
		if r.NodeID == 0 {
			if st := ReadRuleStats(r.Tag); st != nil {
				out[r.Tag] = st
			}
		} else if rep, ok := nodeReports[r.NodeID]; ok && rep.Stats != nil {
			if st, ok2 := rep.Stats[r.Tag]; ok2 && st != nil {
				out[r.Tag] = st
			}
		}
	}
	return out
}

// Sample 采样一次：算差量写桶。计数器回退（实例重启）按新值重新起算。
func (tc *TrafficCollector) Sample() {
	cur := tc.currentTotals()
	now := time.Now()
	tc.mu.Lock()
	defer tc.mu.Unlock()

	//清理已删除规则的基线（保留历史桶）
	for tag := range tc.db.Last {
		if _, ok := cur[tag]; !ok {
			delete(tc.db.Last, tag)
		}
	}

	for tag, st := range cur {
		curVal := bucketVal{Up: st.Up, Down: st.Down, Conns: st.ConnsTotal}
		last, seen := tc.db.Last[tag]
		if !seen {
			tc.db.Last[tag] = curVal
			continue //首次见到该规则，只建立基线
		}
		dUp := deltaOrReset(last.Up, curVal.Up)
		dDown := deltaOrReset(last.Down, curVal.Down)
		dConn := deltaOrReset(last.Conns, curVal.Conns)
		tc.db.Last[tag] = curVal
		if dUp == 0 && dDown == 0 && dConn == 0 {
			continue
		}

		bStart := now.Unix() / trafficBucketSec * trafficBucketSec
		if tc.db.Buckets[tag] == nil {
			tc.db.Buckets[tag] = map[int64]*bucketVal{}
		}
		b := tc.db.Buckets[tag][bStart]
		if b == nil {
			b = &bucketVal{}
			tc.db.Buckets[tag][bStart] = b
		}
		b.Up += dUp
		b.Down += dDown
		b.Conns += dConn

		dayKey := now.Format("2006-01-02")
		if tc.db.Days[tag] == nil {
			tc.db.Days[tag] = map[string]*bucketVal{}
		}
		d := tc.db.Days[tag][dayKey]
		if d == nil {
			d = &bucketVal{}
			tc.db.Days[tag][dayKey] = d
		}
		d.Up += dUp
		d.Down += dDown
		d.Conns += dConn
	}

	tc.pruneLocked(now)
	tc.saveLocked()
}

func deltaOrReset(oldV, newV uint64) uint64 {
	if newV < oldV {
		return newV //计数器重置：实例重启，从零重新累计
	}
	return newV - oldV
}

func (tc *TrafficCollector) pruneLocked(now time.Time) {
	cutB := now.Add(-trafficRawKeep).Unix()
	for tag, m := range tc.db.Buckets {
		for t := range m {
			if t < cutB {
				delete(m, t)
			}
		}
		if len(m) == 0 {
			delete(tc.db.Buckets, tag)
		}
	}
	cutD := now.Add(-trafficDaysKeep).Format("2006-01-02")
	for tag, m := range tc.db.Days {
		for k := range m {
			if k < cutD {
				delete(m, k)
			}
		}
		if len(m) == 0 {
			delete(tc.db.Days, tag)
		}
	}
}

// Run 阻塞采样循环。
func (tc *TrafficCollector) Run() {
	tc.Sample() //启动即采样建立基线
	t := time.NewTicker(tc.interval)
	defer t.Stop()
	for range t.C {
		tc.Sample()
	}
}

// ---------- 查询 ----------

type trafficPoint struct {
	T    int64  `json:"t"`
	Up   uint64 `json:"up"`
	Down uint64 `json:"down"`
}

type trafficRow struct {
	Tag    string `json:"tag"`
	NodeID int64  `json:"node_id"`
	Up     uint64 `json:"up"`
	Down   uint64 `json:"down"`
	Conns  uint64 `json:"conns"`
}

// Query uses hourly points for the last 24 hours and calendar-day totals for longer ranges.
// Daily storage covers all long ranges, including seven days after raw buckets expire.
func (tc *TrafficCollector) Query(rangeKey string, viewer *User) map[string]any {
	return tc.queryAt(rangeKey, viewer, time.Now())
}

func (tc *TrafficCollector) queryAt(rangeKey string, viewer *User, now time.Time) map[string]any {
	days := 0
	switch rangeKey {
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "365d":
		days = 365
	default:
		rangeKey = "24h"
	}
	type ruleMeta struct{ nodeID, owner int64 }
	metas := map[string]ruleMeta{}
	for _, r := range tc.store.ListRules(0, true) {
		metas[r.Tag] = ruleMeta{r.NodeID, r.OwnerID}
	}
	visible := func(tag string) bool {
		if viewer == nil || viewer.IsAdmin {
			return true
		}
		m, ok := metas[tag]
		return ok && m.owner == viewer.ID
	}

	tc.mu.Lock()
	defer tc.mu.Unlock()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayIndex := map[string]int{}
	var points []trafficPoint
	var start time.Time
	if days > 0 {
		start = today.AddDate(0, 0, 1-days)
		stepDays := 1
		if days == 365 {
			stepDays = 7
		}
		points = make([]trafficPoint, (days+stepDays-1)/stepDays)
		for i := range points {
			points[i].T = start.AddDate(0, 0, i*stepDays).Unix()
		}
		for i := 0; i < days; i++ {
			dayIndex[start.AddDate(0, 0, i).Format("2006-01-02")] = i / stepDays
		}
	} else {
		start = now.Add(-24 * time.Hour).Truncate(time.Hour)
		points = make([]trafficPoint, 25)
		for i := range points {
			points[i].T = start.Add(time.Duration(i) * time.Hour).Unix()
		}
	}
	rowMap := map[string]*trafficRow{}
	add := func(tag string, b *bucketVal, idx int) {
		if b == nil || idx < 0 || idx >= len(points) {
			return
		}
		row := rowMap[tag]
		if row == nil {
			row = &trafficRow{Tag: tag, NodeID: metas[tag].nodeID}
			rowMap[tag] = row
		}
		row.Up += b.Up
		row.Down += b.Down
		row.Conns += b.Conns
		points[idx].Up += b.Up
		points[idx].Down += b.Down
	}
	if days == 0 {
		cutoff := now.Add(-24*time.Hour).Unix() / trafficBucketSec * trafficBucketSec
		for tag, buckets := range tc.db.Buckets {
			if !visible(tag) {
				continue
			}
			for at, b := range buckets {
				if at < cutoff || at > now.Unix() {
					continue
				}
				add(tag, b, int((at-start.Unix())/3600))
			}
		}
	}
	heatStart, todayKey := today.AddDate(0, 0, -364).Format("2006-01-02"), today.Format("2006-01-02")
	type dayAgg struct{ Up, Down uint64 }
	heat := map[string]dayAgg{}
	for tag, buckets := range tc.db.Days {
		if !visible(tag) {
			continue
		}
		for day, b := range buckets {
			if b == nil {
				continue
			}
			if day >= heatStart && day <= todayKey {
				a := heat[day]
				a.Up += b.Up
				a.Down += b.Down
				heat[day] = a
			}
			if idx, ok := dayIndex[day]; days > 0 && ok {
				add(tag, b, idx)
			}
		}
	}
	var totUp, totDown, totConn uint64
	rows := make([]*trafficRow, 0, len(rowMap))
	for _, r := range rowMap {
		rows = append(rows, r)
		totUp += r.Up
		totDown += r.Down
		totConn += r.Conns
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Up+rows[i].Down > rows[j].Up+rows[j].Down })
	heatList := make([]map[string]any, 0, len(heat))
	for day, a := range heat {
		heatList = append(heatList, map[string]any{"date": day, "up": a.Up, "down": a.Down})
	}
	sort.Slice(heatList, func(i, j int) bool { return heatList[i]["date"].(string) < heatList[j]["date"].(string) })
	return map[string]any{"range": rangeKey, "points": points, "rows": rows, "heatmap": heatList,
		"totals": map[string]uint64{"up": totUp, "down": totDown, "conns": totConn}}
}

// StartTrafficCollector main 里启动；interval<=0 用默认 60s。
func StartTrafficCollector(store *Store, stateDir string, interval time.Duration) *TrafficCollector {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	tc := LoadTrafficCollector(store, stateDir+"/traffic.json", interval)
	go tc.Run()
	log.Printf("流量采样器已启动（%v 周期，数据 %s/traffic.json）", interval, stateDir)
	return tc
}
