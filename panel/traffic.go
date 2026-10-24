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

// Query 聚合一段时间：points 趋势、rows 分组、heatmap 热力图。
// viewer==nil 或管理员看全部；普通用户只看自己的规则（含其已删规则的历史）。
func (tc *TrafficCollector) Query(rangeKey string, viewer *User) map[string]any {
	now := time.Now()
	var dur time.Duration
	var pointCount int
	switch rangeKey {
	case "24h":
		dur, pointCount = 24*time.Hour, 24
	case "7d":
		dur, pointCount = 7*24*time.Hour, 28
	case "30d":
		dur, pointCount = 30*24*time.Hour, 30
	case "365d":
		dur, pointCount = 365*24*time.Hour, 52
	default:
		rangeKey, dur, pointCount = "24h", 24*time.Hour, 24
	}
	start := now.Add(-dur)

	// 规则归属/节点信息
	type ruleMeta struct {
		nodeID int64
		owner  int64
	}
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

	// 粗范围用日桶，细范围用 5 分钟桶，避免双重计数
	useDays := rangeKey == "30d" || rangeKey == "365d"
	rowMap := map[string]*trafficRow{}
	newRow := func(tag string) *trafficRow {
		if r, ok := rowMap[tag]; ok {
			return r
		}
		m := metas[tag]
		r := &trafficRow{Tag: tag, NodeID: m.nodeID}
		rowMap[tag] = r
		return r
	}

	// 趋势点（全规则合并）
	points := make([]trafficPoint, pointCount)
	var ptStart, ptStep int64
	if rangeKey == "365d" {
		ptStep = 7 * 86400
	} else {
		ptStep = int64(dur.Seconds()) / int64(pointCount)
	}
	ptStart = now.Unix()/ptStep*ptStep - int64(pointCount-1)*ptStep

	if !useDays {
		for tag, buckets := range tc.db.Buckets {
			if !visible(tag) {
				continue
			}
			row := newRow(tag)
			for bs, b := range buckets {
				if bs < start.Unix() {
					continue
				}
				row.Up += b.Up
				row.Down += b.Down
				row.Conns += b.Conns
				idx := (bs - ptStart) / ptStep
				if idx >= 0 && idx < int64(pointCount) {
					points[idx].Up += b.Up
					points[idx].Down += b.Down
				}
			}
		}
	}

	// 日桶：热力图（总是）+ 粗范围的行与趋势点
	heatStart := now.Add(-364 * 24 * time.Hour).Format("2006-01-02")
	rangeStartKey := start.Format("2006-01-02")
	type dayAgg struct{ Up, Down uint64 }
	heat := map[string]dayAgg{}
	for tag, days := range tc.db.Days {
		if !visible(tag) {
			continue
		}
		for dk, d := range days {
			if dk >= heatStart {
				a := heat[dk]
				a.Up += d.Up
				a.Down += d.Down
				heat[dk] = a
			}
			if useDays && dk >= rangeStartKey {
				row := newRow(tag)
				row.Up += d.Up
				row.Down += d.Down
				row.Conns += d.Conns
			}
		}
	}
	if useDays {
		points = make([]trafficPoint, pointCount)
		for dk, a := range heat {
			if dk < rangeStartKey {
				continue
			}
			t, err := time.ParseInLocation("2006-01-02", dk, time.Local)
			if err != nil {
				continue
			}
			idx := (t.Unix() + 12*3600 - ptStart) / ptStep //取当日中午对齐点
			if idx >= 0 && idx < int64(pointCount) {
				points[idx].Up += a.Up
				points[idx].Down += a.Down
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
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Up+rows[i].Down > rows[j].Up+rows[j].Down
	})

	heatList := make([]map[string]any, 0, len(heat))
	for dk, a := range heat {
		heatList = append(heatList, map[string]any{"date": dk, "up": a.Up, "down": a.Down})
	}
	sort.Slice(heatList, func(i, j int) bool {
		return heatList[i]["date"].(string) < heatList[j]["date"].(string)
	})

	return map[string]any{
		"range":   rangeKey,
		"points":  points,
		"rows":    rows,
		"heatmap": heatList,
		"totals":  map[string]uint64{"up": totUp, "down": totDown, "conns": totConn},
	}
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
