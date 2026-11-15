# Relay Panel

多用户 TCP/UDP 转发面板（主从架构）。转发内核使用 [Xeloan/TCP-preconnection-relay](https://github.com/Xeloan/TCP-preconnection-relay)（TCP 预连接池 + splice 零拷贝，在此之上扩展了协议开关 / 流量统计 / 实例限速）。

## 特性

- **多用户**：管理员/普通用户两级权限，普通用户只能看到和操作自己的转发；bcrypt 密码、会话、登录防爆破锁定
- **主从架构**：主控统一下发配置，多台被控节点执行转发；节点出站轮询（正常网络下约 5 秒生效），穿透 NAT，无需开放入站管理端口
- **TLS 加密**：主控自动自签证书启用 HTTPS，节点安装命令自动携带证书 SHA256 指纹固定，防中间人
- **流量体系**：内核计数 → 规则级配额超限自动停机（本地/节点双侧执行）→ 流量统计页（趋势图 / 年度热力图 / 按转发与节点分组占比）
- **实例限速**：内核令牌桶限速（KB/s，上下行分别限制；同方向 TCP 与 UDP 共享额度）
- **TCP + UDP 独立开关**：每条转发可任选 TCP / UDP / 两者
- **一键 BBR + TCP 调优**：安装时按内存自动选档（保守/标准/激进），面板内可切换
- **被控端单文件客户端**：Go 静态二进制内嵌 musl 静态 tcp_pool，免编译；无 systemd 环境（Alpine/OpenRC）自动直跑子进程模式，崩溃自愈
- **批量添加与定期测活**：文本行批量建规则（`[标签:]端口 远端 [远端端口] [tcp|udp]`）；主控与节点双侧 TCP 探测（60s 周期），规则表与拓扑图实时显示时延/超时
- **转发拓扑**：cc-switch 风格流程图（客户端 → 中转节点 → 落地机），连线按运行/停止/探测状态着色并标注时延
- **Docker**：单端口复用（页面/API/节点通信），Cloudflare/反代友好
- **前端**：React + shadcn/ui（`panel/frontend`，Node 构建，产物输出 `panel/web` 由后端 embed）

## 安装

主控（root，需 systemd）：

```bash
curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh && bash panel_install.sh
```

被控节点：主控「节点管理」页添加节点，复制给出的命令在被控机执行即可（优先下载 Releases 预编译单文件客户端，失败回退源码编译）。

> 面板安装（panel_install.sh）只安装 `tcp_pool` 内核、面板二进制和两个 systemd 单元，**不安装任何 `relay` 命令**。仓库根目录的 `install.sh` 是上游内核项目的原始 CLI 安装器，仅供直接使用内核（不要面板）的场景，与面板无关。

## 升级

管理员在「系统调优」页的「版本与升级」卡片里操作：

- **检查更新**：读取本仓库 GitHub Releases 的最新发布。只在打开该页或点击按钮时访问 GitHub，没有后台定时检查。
- **升级主控**：下载与架构匹配的发行包，按同一发布里的 `SHA256SUMS` 校验，并确认其自报版本与标签一致，然后替换面板程序并原地重启（旧版本保留为 `relay-panel.bak`）。Docker 部署不支持，请拉取新镜像重建容器。
- **升级节点**：主控只向节点下发版本标签，节点自行从 GitHub 下载并做同样的校验后重启代理。节点只升不降，也不接受主控给出的下载地址。systemd 节点的转发不中断；直跑模式的节点会短暂重启转发实例。

注意事项：

- 节点从 v1.2.0 起支持远程升级；更早的节点需在节点上重新执行一次安装命令。
- 主控和节点都需要能访问 `github.com`；可在服务环境里设置 `HTTPS_PROXY`。`PANEL_UPDATE_REPO=owner/name` 可改用自己的仓库，`PANEL_SELF_UPDATE=0` 关闭在线升级。
- 校验清单与发行包来自同一个发布，能发现下载损坏，但不等同于签名：发布的可信度取决于仓库的发布权限。
- 主控本机的 `tcp_pool` 内核不随面板升级，仍由安装脚本更新。
- 发布方式：把 `panel/api.go` 的 `panelSemver` 改为新版本号，推送同名标签 `vX.Y.Z`，CI 会构建并上传发行包与 `SHA256SUMS`（标签与版本号不一致时构建失败），同时发布同版本的 Docker 镜像。

## Docker

```bash
docker compose up -d           # 拉取预构建镜像，http://服务器IP:8081
docker compose up -d --build   # 或者用本地源码构建
```

- **镜像**：CI 发布多架构镜像（amd64 / arm64 / armv7）到 `ghcr.io/leoch627/relay-panel`。`latest` 与 `X.Y.Z` 对应 `vX.Y.Z` 标签的发布，`edge` 跟随主分支。
- **升级**：`docker compose pull && docker compose up -d`。容器内不提供「升级主控」，但系统页仍可检查更新并升级被控节点。

- **单端口复用**：页面、面板 API、节点轮询 API 全部走同一个端口（默认 8081），不额外监听
- **反代 / Cloudflare**：镜像默认 `PANEL_TLS=0`，由反代终止 TLS；面板按 `X-Forwarded-Proto` 自动适配 Secure Cookie，访问地址（节点安装命令里的 master URL）自动跟随浏览器当前域名/协议。套 CF 时被控机安装命令**不带** `PANEL_TLS_FP`（CF 边缘证书非固定自签证书，节点用系统 CA 校验即可），CF 回源模式选 Full（接受自签源站证书）——或干脆主控保持 `PANEL_TLS=0` 走 HTTP 回源
- 裸跑直连（无反代）把 compose 里 `PANEL_TLS` 改为 `1`，面板自动生成自签证书并启用 HTTPS
- 容器内无 systemd：本机转发规则建议放到被控节点上；状态/证书在 `panel-data` 卷持久化

## 开发

```bash
# 后端（Go，内嵌前端产物）
cd panel && go build .
PANEL_TLS=0 ./relay-panel -addr :8081 -dry-run -state /tmp/relay-panel-dev/panel.json -conf-dir /tmp/relay-panel-dev/conf -admin-password dev123

# 前端（React + shadcn/ui，Vite）
cd panel/frontend && npm ci && npm run dev   # 开发模式代理 /api 到 127.0.0.1:8081
npm run build                                      # 产物输出 panel/web/
```

转发内核、systemd 模板与原版 `relay` CLI 保持兼容（`/etc/tcp_pool/relays.conf` 双向同步）。

## UDP 转发与限额

创建规则时可选择仅 TCP、仅 UDP，或同时启用两者。UDP-only 只需填写 UDP 远端端口；TCP 和 UDP 可以使用同一监听端口，也可以分别转发到不同的远端端口。批量添加例如：

```text
dns:20053 1.1.1.1 53 udp
20054 1.1.1.1 udp
web:20443 example.com 443 both
```

第二行省略远端端口，使用监听端口 20054；没有标签时自动生成未占用的标签。导出后重新导入会保留各规则的目标节点，显式选择批量目标节点时才覆盖它。

限速 0 表示不限速。启用限速后，上下行各有独立额度，同方向 TCP 和 UDP 共用额度。UDP 保持数据报完整，超出额度的数据报直接丢弃，不等待发送；允许最多一个完整数据报的额外突发，之后等待额度补足。这适用于接受 UDP 丢包的业务。流量统计包含成功转发的 TCP 与 UDP 字节。

配额超限会停止规则，并在节点重启后保持停机。提高配额到已用量以上或取消配额可解除限制；手动“启动”会重新开始计数。普通配置编辑不会清除超限状态。配额按周期检查，允许一个检查周期内的超量。

UDP-only 的测活使用同端口 TCP 连接作为参考，不能据此证明 UDP 服务是否可用；失败会显示弱提示。实际 UDP 可用性需要使用目标协议客户端验证。

删除节点后会显示“清理中”，主控保留节点身份，等待该节点停止实例并确认清理后再移除。离线节点需重新上线完成清理；旧版节点需升级到 v1.1.0 才能确认。通过 HTTPS 代理访问时，在安装命令中选择公共证书校验；直连自签主控时选择固定源站指纹。

## 构建与验证

```bash
python3 tests/test_forwarding.py
python3 tests/test_udp.py             # macOS/Linux：真实 UDP 数据报处理
python3 tests/test_linux.py           # Linux：真实 TCP/UDP、限速、背压及半关闭
cd panel && go test -race ./... && go vet ./...
cd frontend && npm ci && npm test && npm run typecheck && npm run build
```

`build-release.sh` 构建当前前端和三种架构的面板，并生成源码及二进制校验清单。安装器优先源码构建；没有 Go 时只接受清单与当前源码一致的预编译面板。已有旧内核不会仅凭协议开关标记被跳过，必须支持新的 `udp_rate_limit` 能力。
