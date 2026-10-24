# TCP-preconnection-relay

高性能TCP/UDP转发器，类似于realm，gost那种。采用零拷贝转发，性能基本无瓶颈。UDP性能良好。TCP连接采用了预链接方式，让线路鸡和落地鸡长期维持一个连接池，随时取用。故而消除了握手延时（长距离转发，如日本转发美国，效果尤为明显，理论上也可用于内网转发如Po0），客观数据上表现为http延时减少，同时也没有单纯连接复用的种种副作用。有完善的连接回收机制，避免了qos以及内存大量占用。

目前最新版支持出入站v4和v6，出站还支持使用域名。同时实现了单配置多转发，更加方便。

---

## Relay Panel 可视化面板（多用户 / TCP+UDP / 自动 BBR + TCP 调优）

在原转发核心之上内置了一套 Web 管理面板（Go 单二进制、内嵌前端、无外部依赖）：

- **多用户**：管理员/普通用户两级权限，普通用户只能看到和操作自己的转发；bcrypt 密码、7 天会话、登录防爆破锁定
- **主从架构**：主控统一下发配置，多台被控节点执行转发；节点出站轮询（约 5 秒生效），无需开放入站管理端口，可在 NAT 后面
- **TLS 加密**：主控默认自动生成自签证书启用 HTTPS，节点管理页直接给出带证书 SHA256 指纹固定的被控安装命令，防中间人；也可换自己的证书
- **流量统计与配额**：转发内核按实例统计上行/下行/连接数，周期写出统计快照；规则可设流量配额（GB），超限自动停机并在仪表盘告警（参考 Flux Panel 的配额思路）
- **流量统计页（类 cc-switch 用量页）**：趋势堆叠面积图（今天/7天/30天/全年）、GitHub 风格年度热力图、上/下行/连接指标卡、按转发或节点分组占比；数据为主控每分钟采样差量（5 分钟粒度明细保留 48h，日汇总保留 400 天），节点流量随心跳自动汇总
- **实例限速**：内核令牌桶限速（KB/s，双向各自限制），规则级配置
- **TCP + UDP 独立开关**：每条转发可任选 TCP / UDP / 两者（v1.7 新特性 `ENABLE_TCP`/`ENABLE_UDP`）
- **一键 BBR + TCP 调优**：安装时自动开启 BBR + fq 并按内存自动选调优档位（保守/标准/激进），面板内可随时切换；主控与被控安装均默认调优
- **实时状态**：每条转发的 systemd 运行状态、期望状态、journalctl 日志在线查看；远端规则状态由节点心跳上报
- **批量与迁移**：规则批量启停/删除、JSON 导入导出、仪表盘总览
- **无缝迁移**：已有 `/etc/tcp_pool/relays.conf` 的转发在面板首次启动时自动导入；与原版 `relay` CLI 互相兼容
- **高级参数**：面板内可配置连接池、超时、缓冲区等全部底层参数（带取值范围校验）

一键安装（root，需 systemd）：

```bash
curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh && bash panel_install.sh
```

可自定义：

```bash
PANEL_PORT=8081 \
PANEL_ADMIN_PASSWORD=你的初始密码 \
PANEL_TUNE_PROFILE=standard \     # conservative|standard|aggressive，默认按内存自动
bash panel_install.sh

PANEL_SKIP_TUNE=1 bash panel_install.sh   # 跳过调优
```

安装完成后访问 `http://服务器IP:8081`，初始账号 `admin`，初始密码自动生成（见 `/etc/relay-panel/initial-admin-password`，登录后请修改并删除）。面板同时维护 `/etc/tcp_pool/relays.conf` 镜像，`relay` CLI 与 `systemctl` 依然可用。

### 主从架构（多机转发）

一台主控 + N 台被控节点。主控面板「节点管理」页添加节点得到密钥，安装命令自动附带 TLS 指针，在被控机执行：

```bash
curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh
PANEL_SLAVE=1 PANEL_MASTER_URL=https://主控IP:8081 PANEL_NODE_KEY=节点密钥 PANEL_TLS_FP=证书指纹 bash panel_install.sh
```

被控安装优先下载**单文件客户端**（GitHub Releases 预编译，Go 静态二进制内嵌 musl 静态版 tcp_pool）：免 gcc、免编译、支持 **Alpine/任意 glibc 发行版 × amd64/arm64**，自动解压 tcp_pool 并注册服务；下载失败才回退源码编译。也可以完全不用安装脚本：

```bash
# 手动单文件安装（root）
curl -fsSL https://github.com/leoch627/relay-panel/releases/latest/download/relay-panel-client-linux-amd64 -o /usr/local/bin/relay-panel
chmod +x /usr/local/bin/relay-panel
/usr/local/bin/relay-panel -slave -master https://主控IP:8081 -node-key 密钥 -tls-fingerprint 指纹 -self-install
```

被控机运行形态：

- **有 systemd**：实例走 `tcp-pool@<tag>.service` 单元（与主控一致）
- **无 systemd（Alpine/OpenRC 等）**：自动切换直跑模式，tcp_pool 作为子进程托管（崩溃自愈、日志写 `/var/log/relay-panel/`），自启动注册 OpenRC 服务
- 之后在主控「新建转发」时选择该节点即可，配置约 5 秒内下发生效

工作原理与安全说明：

- **TLS 传输**：主控默认自签证书启用 HTTPS（`PANEL_TLS=1`，可 `PANEL_TLS=0` 关闭或用 `PANEL_TLS_CERT/KEY` 换正式证书）；被控机通过 `PANEL_TLS_FP`（证书 SHA256 指纹）固定主控身份，无需 CA 也能防中间人；指纹变更即拒连
- 被控节点只用**出站**连接轮询主控（`POST /api/node/pull`），天然穿透 NAT，无需在节点上开放任何管理端口（转发端口照常放行）
- 每个节点独立密钥（Bearer 认证），可随时在主控侧重置；重置后旧密钥立即失效，被控机保持现有转发不变直到换新密钥重启服务
- **流量配额**在节点本地执行（每轮上报周期检查统计文件），断网也不会超额；主控对本地规则每 15 秒巡检
- 节点把本机各实例状态、流量统计与机器信息心跳上报（`POST /api/node/report`），主控界面实时显示节点在线状态与远端规则运行状态
- 规则变更按版本号增量下发：节点只重启有变化的实例；删除规则时节点自动停机并清理配置
- 被控机日志：`journalctl -u relay-panel-slave -f`；远端转发实例日志在被控机上 `journalctl -u tcp-pool@标签`

手动开发调试：

```bash
cd panel && go build . && ./panel -addr :8081 -dry-run -admin-password dev123
```

`-dry-run` 模式不执行 systemctl/sysctl，用于本地演练。

---

## 安装（原版 CLI 方式）

一键安装（脚本指令已更新，请重新复制）：

```bash
curl -fsSL https://raw.githubusercontent.com/Xeloan/TCP-preconnection-relay/main/install.sh -o install.sh && bash install.sh
```

安装脚本会先安装 `git`，然后克隆仓库，优先读取仓库 `dist/` 里的同架构预编译二进制。当前预编译文件支持 `linux-amd64`、`linux-arm64`、`linux-armv7`，如果没有匹配架构，或者二进制无法运行，会自动回退到本地编译。

如果想强制源码编译：

```bash
TCP_POOL_PREBUILT=0 bash install.sh
```

如果要安装指定 tag/branch 的仓库版本：

```bash
TCP_POOL_VERSION=v1.6 bash install.sh
```

维护者可以通过 GitHub Actions 或本地脚本生成预编译文件并放入 `dist/`。也可以在当前机器手动构建本机架构二进制：

```bash
./build-release.sh
```

安装完成后输入管理命令：

```bash
relay
```

管理脚本支持创建/修改/删除转发、启动/停止实例、查看状态和日志、编辑配置、应用 TCP 调优、更新程序和卸载程序。创建或修改转发时，可以选择是否配置高级参数；不配置则使用程序默认值，不会把默认高级参数写进配置文件。执行 `relay update` 会同步更新主程序和 `relay` 管理脚本，并保留已有配置。

也可以直接使用子命令：

```bash
relay add
relay modify
relay delete
relay list
relay restart
relay logs
relay update
relay uninstall
```


## 常用命令说明

打开管理脚本：
```
relay
```

修改配置文件：
```
nano /etc/tcp_pool/relays.conf
```

应用配置并启动/重启全部转发：
```
tcp-pool-start
```

停止某个实例（把 HK 改成你自己的标签）：
```
systemctl stop tcp-pool@HK
```

禁用某个实例开机自启（把 HK 改成你自己的标签）：
```
systemctl disable tcp-pool@HK
```

查看某个实例日志（把 HK 改成你自己的标签），如果看到一坨Preconnect +1，说明成了：
```
journalctl -u tcp-pool@HK -f
```

## 更新日志
v1.7 新增 `ENABLE_TCP`/`ENABLE_UDP` 独立协议开关（配合 relay-panel 面板）；`--version` 能力探测；relays.conf 兼容新键；内核流量统计（`STATS_FILE` 周期写 JSON 快照）与实例级限速（`RATE_LIMIT_KBPS` 令牌桶）。

v1.6 意外发现mihomo貌似有些情况不会主动发EOF，导致连接数会膨胀，虽然有240s兜底但是不够优雅。于是优化了连接回收机制。

v1.5 优化半连接处理机制，避免了googleplay下载到94%卡住问题。感谢老虎哥发现bug。

v1.4 按照某位群友要求，新增了一键tcp调优。参数比较通用，适用于绝大部分机器。

v1.3 增加了出入站v4和v6支持，出站还支持使用域名。同时实现了单配置多转发，更加方便。旧版的友友们注意按照指南清空下配置。

## 指南
安装过程中会有保姆级指南。

## 效果示例

* 无预链接的转发（使用realm）：
<img width="2337" height="277" alt="image" src="https://github.com/user-attachments/assets/cba16059-ded2-43da-b571-0bcaff2ea70b" />

* 有预链接的转发:
<img width="2559" height="256" alt="image" src="https://github.com/user-attachments/assets/bc78e370-9072-4fb1-90fc-75d2a6304618" />

* 单线程测速：（需要调参）
<img width="2557" height="216" alt="image" src="https://github.com/user-attachments/assets/30c7c92e-c9d1-4f9d-80ee-9b41190a9d8f" />

* gomami最近被干了禁用了udp，实际上没问题，等好了我更新一下图片。

* 测试环境为上海移动，日本优化线路为gomami，美国西雅图落地为Bug Net（名字懒得改了），可见在转发路径高rtt情况下有明显的延时下降，同时单线程速率表现良好，和其它转发无异。

* 日本优化Gomami：https://www.gomami.io (贵，无aff）
* 美国西雅图落地：https://www.misaka.io (贵，无aff，商家也没开aff功能，国际互联非常优秀，但是日本到这家因为ddos，最近不太稳定）
* 美国西雅图落地：https://bug.pw?ref=Nifwr0tPxf （便宜点，日本过去延时稳定82ms，带宽现在比misaka足且稳定，所以有aff）
* 喜欢的话给我买包辣条
<img width="636" height="730" alt="image" src="https://github.com/user-attachments/assets/7a40db31-1e51-4e13-8aea-46f14f8ca6d1" />
