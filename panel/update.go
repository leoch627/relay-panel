package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 在线升级：主控与被控节点都从 GitHub Releases 下载与自身架构匹配的发行包，
// 按同一发布里的 SHA256SUMS 校验后原子替换自身并原地重新执行。
//
// 信任边界：
//   - 下载地址只由本机配置的仓库和 vX.Y.Z 标签拼出，主控下发给节点的只有标签，
//     不能让节点去下载任意地址；
//   - 只升不降，主控被冒充也无法把节点退回到有已知问题的旧版本；
//   - 校验清单与二进制来自同一个发布，能发现下载损坏和被截断，但不能替代签名：
//     发布本身的可信度取决于 GitHub 仓库的发布权限。

const (
	defaultUpdateRepo = "leoch627/relay-panel"
	updateCheckTTL    = 10 * time.Minute
	updateErrorTTL    = time.Minute
	updateForceGap    = 5 * time.Second
	updateRetryAfter  = 10 * time.Minute
	updateMaxBinary   = 128 << 20
	updateTimeout     = 10 * time.Minute
)

var (
	releaseTagRe = regexp.MustCompile(`^v(\d{1,4})\.(\d{1,4})\.(\d{1,4})$`)
	updateRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	sha256HexRe  = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

type semver [3]int

// parseSemver 只接受 vX.Y.Z（前缀 v 可省略），不接受预发布后缀。
func parseSemver(s string) (semver, bool) {
	m := releaseTagRe.FindStringSubmatch("v" + strings.TrimPrefix(s, "v"))
	if m == nil {
		return semver{}, false
	}
	var v semver
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func (a semver) newerThan(b semver) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func (a semver) tag() string { return fmt.Sprintf("v%d.%d.%d", a[0], a[1], a[2]) }

// versionFromBanner 从 `-version` 输出或节点上报的版本串里取出版本号，
// 形如 "relay-panel v1.2.0 (tcp_pool v1.8)"。
func versionFromBanner(s string) (semver, bool) {
	fields := strings.Fields(s)
	if len(fields) < 2 || fields[0] != "relay-panel" {
		return semver{}, false
	}
	return parseSemver(fields[1])
}

func currentSemver() semver {
	v, _ := parseSemver(panelSemver)
	return v
}

// ReleaseInfo 是展示给管理员的最新发布信息。
type ReleaseInfo struct {
	Tag         string `json:"tag"`
	URL         string `json:"url"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
}

type updateStatus struct {
	Phase   string `json:"phase"` // idle | downloading | restarting | failed
	Message string `json:"message"`
	Target  string `json:"target"`
}

type Updater struct {
	repo           string
	apiBase        string
	downloadBase   string
	client         *http.Client
	current        semver
	slave          bool
	disabledReason string // 非空：本进程不能替换自身
	exe            string // 启动时解析一次；替换后 /proc/self/exe 会指向旧文件
	restart        func() error

	mu        sync.Mutex
	cached    *ReleaseInfo
	checkedAt time.Time
	checkErr  error
	status    updateStatus
}

func selfUpdateDisabledReason(dryRun, slave bool) string {
	switch {
	case dryRun:
		return "演练模式不执行升级"
	case os.Getenv("PANEL_SELF_UPDATE") == "0":
		return "已通过 PANEL_SELF_UPDATE=0 关闭在线升级"
	case runtime.GOOS != "linux":
		return "在线升级仅支持 Linux"
	}
	if !slave {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			return "Docker 部署请拉取新镜像并重建容器"
		}
	}
	return ""
}

func NewUpdater(dryRun, slave bool) *Updater {
	repo := os.Getenv("PANEL_UPDATE_REPO")
	if !updateRepoRe.MatchString(repo) {
		if repo != "" {
			log.Printf("[update] 忽略不合法的 PANEL_UPDATE_REPO=%q", repo)
		}
		repo = defaultUpdateRepo
	}
	u := &Updater{
		repo:           repo,
		apiBase:        "https://api.github.com",
		downloadBase:   "https://github.com",
		client:         newUpdateHTTPClient(),
		current:        currentSemver(),
		slave:          slave,
		disabledReason: selfUpdateDisabledReason(dryRun, slave),
		status:         updateStatus{Phase: "idle"},
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		u.exe = exe
	} else if u.disabledReason == "" {
		u.disabledReason = "无法确定自身可执行文件路径"
	}
	u.restart = func() error { return syscall.Exec(u.exe, os.Args, os.Environ()) }
	return u
}

// newUpdateHTTPClient 不设总超时（下载由 context 限时），沿用 HTTPS_PROXY 等环境变量，
// 并禁止跳转时从 https 降级。
func newUpdateHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("跳转次数过多")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("拒绝从 https 跳转到非加密地址")
			}
			return nil
		},
	}
}

func (u *Updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "relay-panel-updater")
	return u.client.Do(req)
}

// assetName 返回当前进程应下载的发行包名。amd64/arm64 节点使用内嵌 tcp_pool 的
// 单文件客户端，其余情况使用普通面板二进制。
func (u *Updater) assetName() (string, error) {
	switch arch := runtime.GOARCH; {
	case u.slave && (arch == "amd64" || arch == "arm64"):
		return "relay-panel-client-linux-" + arch, nil
	case arch == "amd64" || arch == "arm64":
		return "relay-panel-linux-" + arch, nil
	case arch == "arm":
		return "relay-panel-linux-armv7", nil
	default:
		return "", fmt.Errorf("没有适用于 %s 架构的发行包", arch)
	}
}

// Latest 返回最新发布（带缓存）。出错时仍返回上一次成功的结果，供界面继续展示。
func (u *Updater) Latest(ctx context.Context, force bool) (*ReleaseInfo, time.Time, error) {
	u.mu.Lock()
	ttl := updateCheckTTL
	if u.checkErr != nil {
		ttl = updateErrorTTL
	}
	if force {
		ttl = updateForceGap
	}
	if !u.checkedAt.IsZero() && time.Since(u.checkedAt) < ttl {
		rel, at, err := u.cached, u.checkedAt, u.checkErr
		u.mu.Unlock()
		return rel, at, err
	}
	u.mu.Unlock()

	rel, err := u.fetchLatest(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now()
	u.checkErr = err
	if err == nil {
		u.cached = rel
	}
	return u.cached, u.checkedAt, err
}

func (u *Updater) fetchLatest(ctx context.Context) (*ReleaseInfo, error) {
	resp, err := u.get(ctx, u.apiBase+"/repos/"+u.repo+"/releases/latest")
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("仓库 %s 还没有发布版本", u.repo)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub API 限流，请稍后再试")
	default:
		return nil, fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	var body struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("解析发布信息失败: %w", err)
	}
	if !releaseTagRe.MatchString(body.TagName) {
		return nil, fmt.Errorf("最新发布的标签 %q 不是 vX.Y.Z 格式", body.TagName)
	}
	rel := &ReleaseInfo{Tag: body.TagName, Notes: body.Body, PublishedAt: body.PublishedAt}
	// 链接只指向发布页本身，不采信接口返回的任意地址
	rel.URL = u.downloadBase + "/" + u.repo + "/releases/tag/" + body.TagName
	if notes := []rune(rel.Notes); len(notes) > 4000 {
		rel.Notes = string(notes[:4000]) + "…"
	}
	return rel, nil
}

func (u *Updater) releaseAssetURL(tag, name string) string {
	return u.downloadBase + "/" + u.repo + "/releases/download/" + tag + "/" + name
}

// fetchChecksum 从发布的 SHA256SUMS 里取出指定文件的校验和。
func (u *Updater) fetchChecksum(ctx context.Context, tag, asset string) (string, error) {
	resp, err := u.get(ctx, u.releaseAssetURL(tag, "SHA256SUMS"))
	if err != nil {
		return "", fmt.Errorf("下载校验清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载校验清单失败: HTTP %d（%s 可能不含 SHA256SUMS）", resp.StatusCode, tag)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", fmt.Errorf("读取校验清单失败: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !sha256HexRe.MatchString(fields[0]) {
			continue
		}
		if filepath.Base(strings.TrimPrefix(fields[1], "*")) == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("校验清单里没有 %s", asset)
}

// Fetch 下载并校验 tag 对应的发行包，返回与自身同目录的临时文件路径。
// 校验内容：SHA256、可执行、且 `-version` 报告的版本与 tag 一致。
func (u *Updater) Fetch(ctx context.Context, tag string) (string, error) {
	want, ok := parseSemver(tag)
	if !ok || !releaseTagRe.MatchString(tag) {
		return "", fmt.Errorf("版本号 %q 不合法", tag)
	}
	if !want.newerThan(u.current) {
		return "", fmt.Errorf("%s 不高于当前版本 %s，不执行降级", tag, u.current.tag())
	}
	if u.exe == "" {
		return "", errors.New("无法确定自身可执行文件路径")
	}
	asset, err := u.assetName()
	if err != nil {
		return "", err
	}
	sum, err := u.fetchChecksum(ctx, tag, asset)
	if err != nil {
		return "", err
	}

	// 清理上次中断留下的临时文件（同一进程内不会并发下载）
	if stale, _ := filepath.Glob(filepath.Join(filepath.Dir(u.exe), ".relay-panel-update-*")); len(stale) > 0 {
		for _, f := range stale {
			os.Remove(f)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(u.exe), ".relay-panel-update-*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		tmp.Close()
		if !keep {
			os.Remove(tmpName)
		}
	}()

	resp, err := u.get(ctx, u.releaseAssetURL(tag, asset))
	if err != nil {
		return "", fmt.Errorf("下载 %s 失败: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 %s 失败: HTTP %d", asset, resp.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, updateMaxBinary+1))
	if err != nil {
		return "", fmt.Errorf("下载 %s 中断: %w", asset, err)
	}
	if n > updateMaxBinary {
		return "", fmt.Errorf("%s 超过大小上限", asset)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != sum {
		return "", fmt.Errorf("%s 校验和不匹配（期望 %s，实际 %s）", asset, sum, got)
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return "", err
	}

	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out []byte
	for attempt := 0; ; attempt++ {
		out, err = exec.CommandContext(runCtx, tmpName, "-version").Output()
		// 其它协程 fork 出的子进程可能短暂持有刚写完的文件描述符
		if !errors.Is(err, syscall.ETXTBSY) || attempt == 4 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return "", fmt.Errorf("新版本无法在本机运行: %w", err)
	}
	if got, ok := versionFromBanner(string(out)); !ok || got != want {
		return "", fmt.Errorf("发行包自报版本与 %s 不一致: %s", tag, strings.TrimSpace(string(out)))
	}
	keep = true
	return tmpName, nil
}

// Install 用已校验的文件原子替换自身，并尽力保留一份 .bak 供手动回滚。
func (u *Updater) Install(tmp string) error {
	backup := u.exe + ".bak"
	_ = os.Remove(backup)
	if err := os.Link(u.exe, backup); err != nil {
		log.Printf("[update] 备份旧版本到 %s 失败（继续升级）: %v", backup, err)
	}
	if err := os.Rename(tmp, u.exe); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换 %s 失败: %w", u.exe, err)
	}
	return nil
}

func logUpdate(format string, args ...any) { log.Printf("[update] "+format, args...) }

func (u *Updater) Status() updateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *Updater) setStatus(phase, message, target string) {
	u.mu.Lock()
	u.status = updateStatus{Phase: phase, Message: message, Target: target}
	u.mu.Unlock()
}

// StartSelfUpdate 主控自升级：只接受刚才检查到的最新版本，后台下载、替换并重启。
func (u *Updater) StartSelfUpdate(tag string) error {
	if u.disabledReason != "" {
		return errors.New(u.disabledReason)
	}
	u.mu.Lock()
	if u.status.Phase == "downloading" || u.status.Phase == "restarting" {
		u.mu.Unlock()
		return errors.New("升级正在进行中")
	}
	if u.cached == nil || u.cached.Tag != tag {
		u.mu.Unlock()
		return errors.New("版本信息已变化，请重新检查更新")
	}
	if want, _ := parseSemver(tag); !want.newerThan(u.current) {
		u.mu.Unlock()
		return errors.New("当前已是最新版本")
	}
	u.status = updateStatus{Phase: "downloading", Message: "正在下载并校验 " + tag, Target: tag}
	u.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
		defer cancel()
		tmp, err := u.Fetch(ctx, tag)
		if err == nil {
			err = u.Install(tmp)
		}
		if err != nil {
			log.Printf("[update] 升级到 %s 失败: %v", tag, err)
			u.setStatus("failed", err.Error(), tag)
			return
		}
		log.Printf("[update] 已安装 %s，正在重启", tag)
		u.setStatus("restarting", "已安装 "+tag+"，正在重启", tag)
		time.Sleep(1500 * time.Millisecond) // 让界面有机会读到「正在重启」
		if err := u.restart(); err != nil {
			log.Printf("[update] 重启失败: %v", err)
			u.setStatus("failed", "新版本已安装，但自动重启失败，请手动重启面板服务: "+err.Error(), tag)
		}
	}()
	return nil
}
