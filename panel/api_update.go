package main

import (
	"context"
	"net/http"
	"time"
)

// 节点自 v1.2.0 起支持按主控指令在线升级；更早的版本需要重新执行安装命令。
var nodeRemoteUpgradeSince = semver{1, 2, 0}

type nodeUpdateRow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Online    bool   `json:"online"`
	Version   string `json:"version"`    // 节点上报的版本标签，未上报过为空
	UpgradeTo string `json:"upgrade_to"` // 已下发、等待节点执行的目标版本
	Supported bool   `json:"supported"`  // 节点版本是否支持远程升级
	Outdated  bool   `json:"outdated"`   // 低于最新发布
	Error     string `json:"error"`      // 节点最近一次升级失败原因
}

func (a *API) nodeUpdateRows(latest *ReleaseInfo) []nodeUpdateRow {
	var latestVer semver
	haveLatest := false
	if latest != nil {
		latestVer, haveLatest = parseSemver(latest.Tag)
	}
	nodes := a.store.ListNodes()
	rows := make([]nodeUpdateRow, 0, len(nodes))
	for _, n := range nodes {
		if n.Deleting {
			continue
		}
		rep := ParseNodeReport(n)
		row := nodeUpdateRow{ID: n.ID, Name: n.Name, Online: NodeOnline(n), UpgradeTo: n.UpgradeTo}
		if v, ok := versionFromBanner(rep.PanelVersion); ok {
			row.Version = v.tag()
			row.Supported = !nodeRemoteUpgradeSince.newerThan(v)
			row.Outdated = haveLatest && latestVer.newerThan(v)
		}
		if n.UpgradeTo != "" {
			row.Error = rep.UpgradeError
		}
		rows = append(rows, row)
	}
	return rows
}

// handleUpdateStatus 返回当前/最新版本、主控升级进度与各节点版本。
// 只在管理员打开系统页或点击「检查更新」时才会访问 GitHub，没有后台定时检查。
func (a *API) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if a.updater == nil {
		errJSON(w, 503, "在线升级未启用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	latest, checkedAt, err := a.updater.Latest(ctx, r.URL.Query().Get("refresh") == "1")
	resp := map[string]any{
		"current":            a.updater.current.tag(),
		"latest":             latest,
		"has_update":         false,
		"checked_at":         isoTime(checkedAt),
		"check_error":        "",
		"repo":               a.updater.repo,
		"self_update":        a.updater.disabledReason == "",
		"self_update_reason": a.updater.disabledReason,
		"status":             a.updater.Status(),
		"nodes":              a.nodeUpdateRows(latest),
	}
	if err != nil {
		resp["check_error"] = err.Error()
	}
	if latest != nil {
		if v, ok := parseSemver(latest.Tag); ok {
			resp["has_update"] = v.newerThan(a.updater.current)
		}
	}
	writeJSON(w, 200, resp)
}

// handleUpdateApply 升级主控自身到管理员确认过的最新版本。
func (a *API) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	if a.updater == nil {
		errJSON(w, 503, "在线升级未启用")
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if err := readBody(r, &req); err != nil {
		errJSON(w, 400, "请求格式错误")
		return
	}
	if err := a.updater.StartSelfUpdate(req.Version); err != nil {
		errJSON(w, 409, err.Error())
		return
	}
	logUpdate("管理员 %s 发起主控升级到 %s", u.Username, req.Version)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": a.updater.Status()})
}

// handleNodesUpgrade 给节点下发（或取消）升级到最新发布的指令。节点在下次轮询时
// 收到版本标签，自行下载、校验并重启；达到目标版本后标记自动清除。
func (a *API) handleNodesUpgrade(w http.ResponseWriter, r *http.Request) {
	u, _, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	if a.updater == nil {
		errJSON(w, 503, "在线升级未启用")
		return
	}
	var req struct {
		IDs    []int64 `json:"ids"`
		All    bool    `json:"all"`
		Cancel bool    `json:"cancel"`
	}
	if err := readBody(r, &req); err != nil || (!req.All && len(req.IDs) == 0) {
		errJSON(w, 400, "请求格式错误或未选择节点")
		return
	}
	var target string
	var targetVer semver
	if !req.Cancel {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		latest, _, _ := a.updater.Latest(ctx, false)
		if latest == nil {
			errJSON(w, 409, "尚未获取到最新版本，请先检查更新")
			return
		}
		target = latest.Tag
		targetVer, _ = parseSemver(target)
	}
	selected := map[int64]bool{}
	for _, id := range req.IDs {
		selected[id] = true
	}
	queued, skipped := 0, 0
	for _, n := range a.store.ListNodes() {
		if n.Deleting || (!req.All && !selected[n.ID]) {
			continue
		}
		if !req.Cancel {
			v, known := versionFromBanner(ParseNodeReport(n).PanelVersion)
			// 版本未知、不支持远程升级或已不低于目标的节点跳过
			if !known || nodeRemoteUpgradeSince.newerThan(v) || !targetVer.newerThan(v) {
				skipped++
				continue
			}
		}
		if err := a.store.SetNodeUpgrade(n.ID, target); err != nil {
			errJSON(w, 500, "保存失败: "+err.Error())
			return
		}
		queued++
	}
	if queued > 0 && !req.Cancel {
		logUpdate("管理员 %s 要求 %d 个节点升级到 %s", u.Username, queued, target)
	}
	writeJSON(w, 200, map[string]any{"queued": queued, "skipped": skipped, "target": target})
}
