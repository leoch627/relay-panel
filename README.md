# Relay Panel

多用户 TCP/UDP 转发面板（主从架构）。转发内核使用 [Xeloan/TCP-preconnection-relay](https://github.com/Xeloan/TCP-preconnection-relay)（TCP 预连接池 + splice 零拷贝，在此之上扩展了协议开关 / 流量统计 / 实例限速）。

## 特性

- **多用户**：管理员/普通用户两级权限，普通用户只能看到和操作自己的转发；bcrypt 密码、会话、登录防爆破锁定
- **主从架构**：主控统一下发配置，多台被控节点执行转发；节点出站轮询（约 5 秒生效），穿透 NAT，无需开放入站管理端口
- **TLS 加密**：主控自动自签证书启用 HTTPS，节点安装命令自动携带证书 SHA256 指纹固定，防中间人
- **流量体系**：内核计数 → 规则级配额超限自动停机（本地/节点双侧执行）→ 流量统计页（趋势图 / 年度热力图 / 按转发与节点分组占比）
- **实例限速**：内核令牌桶限速（KB/s，双向各自限制）
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

## Docker

```bash
docker compose up -d --build   # http://服务器IP:8081
```

- **单端口复用**：页面、面板 API、节点轮询 API 全部走同一个端口（默认 8081），不额外监听
- **反代 / Cloudflare**：镜像默认 `PANEL_TLS=0`，由反代终止 TLS；面板按 `X-Forwarded-Proto` 自动适配 Secure Cookie，访问地址（节点安装命令里的 master URL）自动跟随浏览器当前域名/协议。套 CF 时被控机安装命令**不带** `PANEL_TLS_FP`（CF 边缘证书非固定自签证书，节点用系统 CA 校验即可），CF 回源模式选 Full（接受自签源站证书）——或干脆主控保持 `PANEL_TLS=0` 走 HTTP 回源
- 裸跑直连（无反代）把 compose 里 `PANEL_TLS` 改为 `1`，面板自动生成自签证书并启用 HTTPS
- 容器内无 systemd：本机转发规则建议放到被控节点上；状态/证书在 `panel-data` 卷持久化

## 开发

```bash
# 后端（Go，内嵌前端产物）
cd panel && go build .
./relay-panel -addr :8081 -dry-run -admin-password dev123

# 前端（React + shadcn/ui，Vite）
cd panel/frontend && npm install && npm run dev   # 开发模式代理 /api 到 127.0.0.1:8081
npm run build                                      # 产物输出 panel/web/
```

转发内核、systemd 模板与原版 `relay` CLI 保持兼容（`/etc/tcp_pool/relays.conf` 双向同步）。
