package main

import (
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

// importRelays 解析原版 relays.conf（[tag] 分节 + KEY=VAL），老用户首次使用面板时平滑迁移。
func importRelays(path string) []*Rule {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var rules []*Rule
	var cur *Rule
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tag := line[1 : len(line)-1]
			if !tagRe.MatchString(tag) {
				cur = nil
				continue
			}
			cur = &Rule{EnableTCP: true, EnableUDP: true, Advanced: map[string]int{}}
			cur.Tag = tag
			rules = append(rules, cur)
			continue
		}
		if cur == nil {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k, v := kv[0], strings.TrimSpace(kv[1])
		switch k {
		case "LOCAL_IP":
			cur.ListenIP = v
		case "LOCAL_PORT", "REMOTE_TCP_PORT", "REMOTE_UDP_PORT":
			n, _ := strconv.Atoi(v)
			switch k {
			case "LOCAL_PORT":
				cur.ListenPort = n
			case "REMOTE_TCP_PORT":
				cur.RemoteTCPPort = n
			case "REMOTE_UDP_PORT":
				cur.RemoteUDPPort = n
			}
		case "REMOTE_IP":
			cur.RemoteHost = v
		case "ENABLE_TCP":
			cur.EnableTCP = v != "0"
		case "ENABLE_UDP":
			cur.EnableUDP = v != "0"
		default:
			if _, ok := advancedSpec[k]; ok {
				if n, err := strconv.Atoi(v); err == nil {
					cur.Advanced[k] = n
				}
			}
		}
	}
	// 过滤缺关键字段的残缺分节
	out := rules[:0]
	for _, r := range rules {
		if r.ListenIP != "" && validPort(r.ListenPort) && r.RemoteHost != "" &&
			(validPort(r.RemoteTCPPort) || validPort(r.RemoteUDPPort)) {
			if r.RemoteUDPPort == 0 {
				r.RemoteUDPPort = r.RemoteTCPPort
			}
			out = append(out, r)
		}
	}
	return out
}

func main() {
	addrDefault := os.Getenv("PANEL_ADDR")
	if addrDefault == "" {
		addrDefault = ":8081"
	}
	adminPwEnv := os.Getenv("PANEL_ADMIN_PASSWORD")

	var (
		addr          = flag.String("addr", addrDefault, "监听地址")
		stateFile     = flag.String("state", "", "状态文件路径（默认 /etc/relay-panel/panel.json）")
		confDir       = flag.String("conf-dir", "", "tcp_pool 配置目录（默认 /etc/tcp_pool）")
		dryRun        = flag.Bool("dry-run", false, "演练模式：不真正执行 systemctl/sysctl")
		adminUser     = flag.String("admin-user", "admin", "初始管理员用户名")
		adminPassword = flag.String("admin-password", "", "初始管理员密码（默认取 PANEL_ADMIN_PASSWORD 环境变量，否则自动生成）")
		slaveMode     = flag.Bool("slave", false, "被控节点模式：轮询主控拉取转发配置并管理本机实例")
		masterURL     = flag.String("master", "", "被控模式：主控面板地址，如 https://master:8081")
		nodeKey       = flag.String("node-key", "", "被控模式：节点密钥（主控节点管理页生成）")
		tlsFp         = flag.String("tls-fingerprint", "", "被控模式：主控证书 SHA256 指纹（hex），用于自签证书防中间人")
		tlsInsecure   = flag.Bool("tls-insecure", false, "被控模式：跳过主控证书校验（不推荐）")
		tlsCert       = flag.String("tls-cert", "", "主控：PEM 证书路径（不填则自动生成自签，PANEL_TLS=0 关闭）")
		tlsKey        = flag.String("tls-key", "", "主控：PEM 私钥路径")
		directMode    = flag.Bool("direct", false, "被控模式：tcp_pool 以子进程直跑（默认自动探测，无 systemd 时自动启用，Alpine 可用）")
		tcpoolBin     = flag.String("tcpool-bin", "", "被控模式：tcp_pool 二进制路径（默认 /root/tcp_pool）")
		selfInstall   = flag.Bool("self-install", false, "被控模式：自安装（解压内嵌 tcp_pool + 注册自启动服务）后退出")
		showVersion   = flag.Bool("version", false, "打印版本信息")
	)
	flag.Parse()

	if *showVersion {
		embed := "无"
		if tcpoolEmbedded() {
			embed = fmt.Sprintf("内嵌 tcp_pool %d 字节 (musl static)", len(embeddedTCPPool))
		}
		fmt.Printf("%s（tcp_pool 内嵌: %s）\n", panelVersion, embed)
		return
	}
	if *adminPassword == "" {
		*adminPassword = adminPwEnv
	}

	paths := DefaultPaths()
	if *stateFile != "" {
		paths.StateFile = *stateFile
	}
	if *confDir != "" {
		paths.ConfDir = *confDir
		paths.RelaysConf = filepath.Join(*confDir, "relays.conf")
	}
	if *tcpoolBin != "" {
		paths.TCPPoolBin = *tcpoolBin
	}

	run := &Runner{DryRun: *dryRun}

	// ---------- 被控节点模式 ----------
	if *slaveMode {
		if *masterURL == "" || *nodeKey == "" {
			log.Fatalf("被控模式需要 -master 与 -node-key 参数")
		}
		direct := *directMode
		if !direct && !hasSystemd() {
			direct = true
			log.Printf("[slave] 未检测到 systemd，启用直跑模式（tcp_pool 作为子进程）")
		}
		if !*dryRun {
			if !direct {
				_ = writeFileAtomic("/etc/systemd/system/tcp-pool@.service", []byte(serviceTemplate), 0o644)
			}
			_ = os.MkdirAll(paths.ConfDir, 0o755)
			_ = os.MkdirAll(filepath.Dir("/etc/relay-panel/slave-state.json"), 0o700)
		}
		var tlsCfg *tls.Config
		if strings.HasPrefix(*masterURL, "https://") {
			tlsCfg = SlaveTLSClient(*tlsFp, *tlsInsecure)
			if *tlsFp == "" && !*tlsInsecure {
				log.Printf("[slave] 警告：HTTPS 主控未指定 -tls-fingerprint，将使用系统 CA 校验（自签证书会连接失败）")
			}
		}
		slave := NewSlave(*masterURL, *nodeKey, run, paths, tlsCfg, direct)
		if *stateFile != "" {
			slave.statePath = filepath.Join(filepath.Dir(*stateFile), "slave-state.json")
		}
		if *selfInstall {
			self, _ := os.Executable()
			if err := slave.SelfInstall(self); err != nil {
				log.Fatalf("[slave] 自安装失败: %v", err)
			}
			return
		}
		slave.Run()
		return
	}

	store, err := LoadStore(paths.StateFile)
	if err != nil {
		log.Fatalf("加载状态失败: %v", err)
	}

	// 首次运行：创建初始管理员
	if len(store.ListUsers()) == 0 {
		pw := *adminPassword
		if pw == "" {
			buf := make([]byte, 8)
			if _, err := rand.Read(buf); err != nil {
				log.Fatalf("生成随机密码失败: %v", err)
			}
			pw = hex.EncodeToString(buf)
			// 同时落盘一份，防止错过控制台输出
			hintFile := filepath.Join(filepath.Dir(paths.StateFile), "initial-admin-password")
			_ = writeFileAtomic(hintFile, []byte(fmt.Sprintf("%s / %s\n", *adminUser, pw)), 0o600)
			log.Printf("初始管理员已创建，凭据同时写入 %s（登录后请尽快删除该文件并修改密码）", hintFile)
		}
		if _, err := store.CreateUser(*adminUser, pw, true); err != nil {
			log.Fatalf("创建初始管理员失败: %v", err)
		}
		log.Printf("==============================================")
		log.Printf(" 初始管理员: %s", *adminUser)
		log.Printf(" 初始密码  : %s", pw)
		log.Printf("==============================================")
	}

	sd := NewSystemdManager(run, paths)
	tuner := NewTuner(run, paths)

	// 老配置迁移：面板里还没有任何规则时，导入既有 relays.conf
	if len(store.ListRules(0, true)) == 0 {
		if imported := importRelays(paths.RelaysConf); len(imported) > 0 {
			var owner int64
			if users := store.ListUsers(); len(users) > 0 {
				for _, u := range users {
					if u.IsAdmin {
						owner = u.ID
						break
					}
				}
				if owner == 0 {
					owner = users[0].ID
				}
			}
			for _, r := range imported {
				r.OwnerID = owner
				if err := store.UpsertRule(r); err != nil {
					log.Printf("导入转发 %s 失败: %v", r.Tag, err)
				}
			}
			log.Printf("已从 %s 导入 %d 条既有转发", paths.RelaysConf, len(imported))
		}
	}

	if !*dryRun {
		// 确保 systemd 模板存在（与原版安装脚本相同路径）
		_ = writeFileAtomic("/etc/systemd/system/tcp-pool@.service", []byte(serviceTemplate), 0o644)
		_ = os.MkdirAll(paths.ConfDir, 0o755)
		if err := sd.SyncAll(store.ListRules(0, true)); err != nil {
			log.Printf("启动同步配置失败: %v", err)
		}
		sd.DaemonReload()
		// 本机规则配额巡检（15s 周期）
		go StartQuotaEnforcer(store, sd, *dryRun)
	}

	// TLS：PANEL_TLS=0 关闭；默认开启（自动自签，或使用外部证书）
	tlsMode := os.Getenv("PANEL_TLS")
	if tlsMode == "" {
		tlsMode = "1"
	}
	var tlsCfg *TLSConfig
	if tlsMode != "0" {
		stateDir := filepath.Dir(paths.StateFile)
		tlsCfg, err = LoadTLS(tlsMode, *tlsCert, *tlsKey, stateDir)
		if err != nil {
			log.Fatalf("TLS 初始化失败: %v", err)
		}
		if tlsCfg.Generated {
			log.Printf("TLS 已启用（自动自签证书）")
			log.Printf("证书指纹: %s", tlsCfg.Fingerprint)
			log.Printf("被控机连接请加 -tls-fingerprint %s", tlsCfg.Fingerprint)
		} else {
			log.Printf("TLS 已启用（外部证书 %s）指纹: %s", tlsCfg.CertFile, tlsCfg.Fingerprint)
		}
	} else {
		log.Printf("TLS 未启用（PANEL_TLS=0），节点通信为明文 HTTP")
	}

	api := &API{store: store, sd: sd, tuner: tuner, run: run, sessions: NewSessionCache(), tls: tlsCfg}
	// 流量采样器（主控汇总本机 + 各节点上报的计数差量）
	sampleInterval := 60 * time.Second
	if v := os.Getenv("PANEL_SAMPLE_INTERVAL"); v != "" {
		if sec, perr := strconv.Atoi(v); perr == nil && sec >= 5 {
			sampleInterval = time.Duration(sec) * time.Second
		}
	}
	api.traffic = StartTrafficCollector(store, filepath.Dir(paths.StateFile), sampleInterval)
	// 定期测活（主控侧，60s 周期；远端规则另有节点侧探测）
	api.prober = StartProber(store, *dryRun, 60*time.Second)
	mux := http.NewServeMux()
	api.Register(mux)

	// 内嵌前端
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("内嵌前端失败: %v", err)
	}
	mux.Handle("/", http.FileServerFS(sub))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	scheme := "http"
	if tlsCfg != nil {
		scheme = "https"
	}
	log.Printf("%s 监听 %s://%s（dry-run=%v）", panelVersion, scheme, *addr, *dryRun)
	if tlsCfg != nil {
		err = srv.ListenAndServeTLS(tlsCfg.CertFile, tlsCfg.KeyFile)
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil {
		log.Fatalf("HTTP 服务退出: %v", err)
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}
