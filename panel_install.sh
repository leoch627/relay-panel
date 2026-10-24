#!/bin/bash
# relay-panel 一键安装脚本
# 用法：bash panel_install.sh
# 主控模式环境变量：
#   PANEL_REPO          源码仓库（默认 Xeloan/TCP-preconnection-relay）
#   PANEL_PORT          面板端口（默认 8081）
#   PANEL_ADMIN_PASSWORD 初始 admin 密码（默认自动生成）
#   PANEL_SKIP_TUNE=1   跳过 BBR + TCP 调优
#   PANEL_TUNE_PROFILE  调优档位 conservative|standard|aggressive（默认按内存自动选择）
# 被控节点模式环境变量（PANEL_SLAVE=1）：
#   PANEL_MASTER_URL    主控面板地址，如 https://master-ip:8081
#   PANEL_NODE_KEY      节点密钥（主控「节点管理」页生成）
#   PANEL_TLS_FP        主控证书 SHA256 指纹（主控启用 TLS 时由节点管理页附带）
set -euo pipefail

PANEL_REPO="${PANEL_REPO:-leoch627/relay-panel}"
PANEL_PORT="${PANEL_PORT:-8081}"
PANEL_SLAVE="${PANEL_SLAVE:-0}"
PANEL_MASTER_URL="${PANEL_MASTER_URL:-}"
PANEL_NODE_KEY="${PANEL_NODE_KEY:-}"
PANEL_TLS="${PANEL_TLS:-1}"
PANEL_TLS_FP="${PANEL_TLS_FP:-}"
PANEL_BIN=/usr/local/bin/relay-panel
TCPPool_BIN=/root/tcp_pool
STATE_DIR=/etc/relay-panel
CONF_DIR=/etc/tcp_pool
TUNE_SYSCTL=/etc/sysctl.d/99-custom-network-tuning.conf

if [ "$PANEL_SLAVE" = "1" ]; then
    [ -n "$PANEL_MASTER_URL" ] && [ -n "$PANEL_NODE_KEY" ] || {
        echo "被控模式需要 PANEL_MASTER_URL 和 PANEL_NODE_KEY（在主控「节点管理」页生成）" >&2
        exit 1
    }
    echo "=========================================="
    echo " relay-panel 被控节点安装（主控: $PANEL_MASTER_URL）"
    echo "=========================================="
else
    echo "=========================================="
    echo " relay-panel 一键安装（多用户 TCP/UDP 转发面板 · 主控）"
    echo "=========================================="
fi

[ "$(id -u)" -eq 0 ] || { echo "请使用 root 运行：sudo bash panel_install.sh"; exit 1; }

# systemd 只在需要时强制（被控端支持直跑/OpenRC 模式，可无 systemd）
if [ "$PANEL_SLAVE" != "1" ]; then
    command -v systemctl >/dev/null 2>&1 || { echo "错误：主控需要 systemd；被控节点支持无 systemd 环境（PANEL_SLAVE=1）"; exit 1; }
fi

# ---------- 依赖（编译路径才需要；单文件客户端快路径可跳过） ----------
APT_UPDATED=0
install_build_deps() {
    command -v apt >/dev/null 2>&1 || return 0
    local need=()
    for pkg in gcc make curl ca-certificates; do
        command -v "$pkg" >/dev/null 2>&1 || dpkg -s "$pkg" >/dev/null 2>&1 || need+=("$pkg")
    done
    if [ "${#need[@]}" -gt 0 ]; then
        [ "$APT_UPDATED" -eq 0 ] && { apt update; APT_UPDATED=1; }
        apt install -y "${need[@]}"
    fi
    dpkg -s build-essential >/dev/null 2>&1 || apt install -y build-essential
}

# ---------- 架构 ----------
ARCH=""
case "$(uname -m)" in
    x86_64|amd64)  ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    armv7l|armv7*)  ARCH="armv7" ;;
    *) echo "不支持的架构: $(uname -m)"; exit 1 ;;
esac
echo "架构: linux-$ARCH"

# ---------- 源码（按需获取：主控立即克隆；被控端单文件快路径成功则完全不需要源码） ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC_DIR=""
CLONED=0
cleanup_src() { [ "$CLONED" -eq 1 ] && [ -n "$SRC_DIR" ] && rm -rf "$SRC_DIR"; }
trap cleanup_src EXIT

get_source() {
    [ -n "$SRC_DIR" ] && return 0
    if [ -f "$SCRIPT_DIR/panel/main.go" ] && [ -f "$SCRIPT_DIR/tcp_pool.c" ]; then
        SRC_DIR="$SCRIPT_DIR"   # 从仓库目录直接安装，不做清理
        echo "使用本地源码目录: $SRC_DIR"
        return 0
    fi
    command -v git >/dev/null 2>&1 || {
        command -v apt >/dev/null 2>&1 && { apt update; apt install -y git ca-certificates; }
    }
    SRC_DIR="$(mktemp -d /tmp/relay-panel-src.XXXXXX)"
    CLONED=1
    echo "克隆仓库 $PANEL_REPO ..."
    git clone --depth 1 "https://github.com/${PANEL_REPO}.git" "$SRC_DIR"
}

if [ "$PANEL_SLAVE" != "1" ]; then
    get_source   # 主控需要源码编译 tcp_pool
fi

# ---------- 安装底层 tcp_pool ----------
# 面板需要 ENABLE_TCP/ENABLE_UDP 特性（tcp_pool v1.7+），安装时以 --version 输出确认
tcp_pool_has_features() {
    "$1" --version 2>/dev/null | grep -q "enable_tcp"
}

install_tcp_pool() {
    install_build_deps   #编译路径才装 gcc/build-essential
    echo "编译转发核心 tcp_pool ..."
    local tmp
    tmp="$(mktemp /root/tcp_pool.build.XXXXXX)"
    if gcc -O2 -pthread -o "$tmp" "$SRC_DIR/tcp_pool.c"; then
        chmod +x "$tmp"
        if tcp_pool_has_features "$tmp"; then
            mv -f "$tmp" "$TCPPool_BIN"
            echo "tcp_pool 编译完成（含协议开关特性）"
            return 0
        fi
    fi
    rm -f "$tmp"

    # 编译失败或无特性时回退到仓库预编译二进制
    local prebuilt="$SRC_DIR/dist/tcp_pool-linux-$ARCH"
    if [ -f "$prebuilt" ] && tcp_pool_has_features "$prebuilt"; then
        cp -f "$prebuilt" "$TCPPool_BIN"
        chmod +x "$TCPPool_BIN"
        echo "tcp_pool 使用预编译二进制: linux-$ARCH"
        return 0
    fi
    echo "错误：tcp_pool 安装失败（编译失败且无可用预编译包）" >&2
    return 1
}

if [ "$PANEL_SLAVE" = "1" ]; then
    # ---------- 被控端快路径：优先单文件客户端（免 gcc / 免编译） ----------
    # 单文件客户端：GitHub Releases 预编译（Go 静态 + 内嵌 musl tcp_pool，Alpine 可用）
    install_client_prebuilt() {
        local url="${PANEL_CLIENT_URL:-https://github.com/${PANEL_REPO}/releases/latest/download/relay-panel-client-linux-${ARCH}}"
        command -v curl >/dev/null 2>&1 || return 1
        echo "尝试下载单文件客户端: $url"
        if ! curl -fsSL --connect-timeout 10 -o "$PANEL_BIN" "$url"; then
            echo "下载失败，回退源码安装。"
            return 1
        fi
        chmod +x "$PANEL_BIN"
        if "$PANEL_BIN" -version 2>/dev/null | grep -q "relay-panel"; then
            echo "单文件客户端就绪（内嵌 musl tcp_pool，免编译）。"
            return 0
        fi
        echo "客户端无法运行，回退源码安装。"
        rm -f "$PANEL_BIN"
        return 1
    }

    if install_client_prebuilt; then
        CLIENT_SINGLE_FILE=1
    else
        CLIENT_SINGLE_FILE=0
        get_source   #快路径失败，回退源码安装
        if [ -x "$TCPPool_BIN" ] && tcp_pool_has_features "$TCPPool_BIN"; then
            echo "tcp_pool 已是新版，跳过编译"
        else
            install_tcp_pool
        fi

        # ---------- 安装面板 ----------
        install_panel_bin() {
            local prebuilt="$SRC_DIR/dist/relay-panel-linux-$ARCH"
            if [ -f "$prebuilt" ] && "$prebuilt" -h >/dev/null 2>&1; then
                install -m 755 "$prebuilt" "$PANEL_BIN"
                echo "面板二进制: dist/relay-panel-linux-$ARCH"
                return 0
            fi
            if command -v go >/dev/null 2>&1; then
                echo "从源码构建面板 ..."
                (cd "$SRC_DIR/panel" && go build -trimpath -o "$PANEL_BIN" .)
                return 0
            fi
            echo "错误：找不到可用的面板二进制，且本机无 Go 编译器" >&2
            return 1
        }
        install_panel_bin
    fi
else
    # ---------- 主控 ----------
    if [ -x "$TCPPool_BIN" ] && tcp_pool_has_features "$TCPPool_BIN"; then
        echo "tcp_pool 已是新版，跳过编译"
    else
        install_tcp_pool
    fi

    install_panel_bin() {
        local prebuilt="$SRC_DIR/dist/relay-panel-linux-$ARCH"
        if [ -f "$prebuilt" ] && "$prebuilt" -h >/dev/null 2>&1; then
            install -m 755 "$prebuilt" "$PANEL_BIN"
            echo "面板二进制: dist/relay-panel-linux-$ARCH"
            return 0
        fi
        if command -v go >/dev/null 2>&1; then
            echo "从源码构建面板 ..."
            (cd "$SRC_DIR/panel" && go build -trimpath -o "$PANEL_BIN" .)
            return 0
        fi
        echo "错误：找不到可用的面板二进制，且本机无 Go 编译器" >&2
        return 1
    }
    install_panel_bin
fi

# ---------- 配置目录 ----------
mkdir -p "$STATE_DIR" "$CONF_DIR"
chmod 700 "$STATE_DIR"

# ---------- systemd ----------
if command -v systemctl >/dev/null 2>&1; then
cat > /etc/systemd/system/tcp-pool@.service <<'EOF'
[Unit]
Description=High Performance TCP Connection Pool (C Version)
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/root/tcp_pool
EnvironmentFile=/etc/tcp_pool/%i.conf

Nice=-10
LimitNOFILE=1048576

Restart=always
RestartSec=3
User=root
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF
fi

if [ "$PANEL_SLAVE" = "1" ]; then
    FP_ARG=""
    [ -n "$PANEL_TLS_FP" ] && FP_ARG="-tls-fingerprint $PANEL_TLS_FP"

    if ! command -v systemctl >/dev/null 2>&1; then
        # 无 systemd（Alpine/OpenRC 等）：让单文件客户端自安装（自动检测 init 系统）
        echo "未检测到 systemd，使用客户端自安装（OpenRC/直跑）..."
        "$PANEL_BIN" -slave -master "$PANEL_MASTER_URL" -node-key "$PANEL_NODE_KEY" $FP_ARG -self-install
        echo "被控节点安装完成（非 systemd 模式）。"
        echo "日志：tail -f /var/log/relay-panel/*.log"
        exit 0
    fi

    cat > /etc/systemd/system/relay-panel-slave.service <<EOF
[Unit]
Description=Relay Panel Slave Node Agent
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=$PANEL_BIN -slave -master $PANEL_MASTER_URL -node-key $PANEL_NODE_KEY $FP_ARG
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable relay-panel-slave
    systemctl restart relay-panel-slave
    sleep 1
    systemctl is-active relay-panel-slave >/dev/null || { journalctl -u relay-panel-slave -n 30 --no-pager; exit 1; }
else
    if [ ! -f "$STATE_DIR/panel.env" ]; then
        cat > "$STATE_DIR/panel.env" <<EOF
# relay-panel 环境变量（仅首次创建管理员时读取密码）
PANEL_ADDR=:${PANEL_PORT}
PANEL_ADMIN_PASSWORD=${PANEL_ADMIN_PASSWORD:-}
# TLS：1=自动自签并启用（节点管理页会展示指纹）；0=明文 HTTP
PANEL_TLS=${PANEL_TLS}
EOF
        chmod 600 "$STATE_DIR/panel.env"
    else
        sed -i "s/^PANEL_ADDR=.*/PANEL_ADDR=:${PANEL_PORT}/" "$STATE_DIR/panel.env"
        sed -i "s/^PANEL_TLS=.*/PANEL_TLS=${PANEL_TLS}/" "$STATE_DIR/panel.env"
    fi

    cat > /etc/systemd/system/relay-panel.service <<EOF
[Unit]
Description=Relay Panel Web UI
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=$PANEL_BIN
EnvironmentFile=-$STATE_DIR/panel.env
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable relay-panel
    systemctl restart relay-panel
    sleep 1
    systemctl is-active relay-panel >/dev/null || { journalctl -u relay-panel -n 30 --no-pager; exit 1; }
fi

# ---------- BBR + TCP 自动调优 ----------
if [ "${PANEL_SKIP_TUNE:-0}" != "1" ]; then
    PROFILE="${PANEL_TUNE_PROFILE:-}"
    if [ -z "$PROFILE" ]; then
        MEM_KB="$(awk '/MemTotal/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
        if [ "$MEM_KB" -lt 900000 ]; then
            PROFILE="conservative"
        else
            PROFILE="standard"
        fi
    fi

    echo "应用 TCP 调优（$PROFILE）并开启 BBR ..."

    # BBR
    if ! grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null; then
        modprobe tcp_bbr 2>/dev/null || true
    fi
    mkdir -p /etc/modules-load.d
    echo "tcp_bbr" > /etc/modules-load.d/tcp-bbr.conf

    CC="cubic"
    if grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null; then
        CC="bbr"
        echo "BBR 可用，已选择 bbr。"
    else
        echo "警告：当前内核不支持 BBR，回退 cubic（内核 >= 4.9 才内置 BBR）。"
    fi

    if [ "$PROFILE" = "conservative" ]; then
        cat > "$TUNE_SYSCTL" <<EOF
# relay-panel TCP 调优 (conservative / 保守)
net.ipv4.tcp_congestion_control = $CC
net.core.default_qdisc = fq
net.core.somaxconn = 16384
net.ipv4.tcp_max_syn_backlog = 16384
net.core.netdev_max_backlog = 16384
net.core.rmem_max = 33554432
net.core.wmem_max = 33554432
net.core.rmem_default = 262144
net.core.wmem_default = 262144
net.ipv4.tcp_rmem = 4096 262144 33554432
net.ipv4.tcp_wmem = 4096 262144 33554432
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_fin_timeout = 15
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_no_metrics_save = 1
net.ipv4.tcp_retries2 = 8
net.ipv4.tcp_syncookies = 1
net.ipv4.ip_local_port_range = 1024 65535
EOF
    elif [ "$PROFILE" = "aggressive" ]; then
        cat > "$TUNE_SYSCTL" <<EOF
# relay-panel TCP 调优 (aggressive / 激进)
net.ipv4.tcp_congestion_control = $CC
net.core.default_qdisc = fq
net.core.somaxconn = 262144
net.ipv4.tcp_max_syn_backlog = 262144
net.core.netdev_max_backlog = 500000
net.core.rmem_max = 268435456
net.core.wmem_max = 268435456
net.core.rmem_default = 4194304
net.core.wmem_default = 4194304
net.ipv4.tcp_rmem = 4096 4194304 268435456
net.ipv4.tcp_wmem = 4096 4194304 268435456
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_fin_timeout = 10
net.ipv4.tcp_mtu_probing = 2
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_no_metrics_save = 1
net.ipv4.tcp_retries2 = 8
net.ipv4.tcp_timestamps = 1
net.ipv4.tcp_sack = 1
net.ipv4.tcp_syncookies = 1
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 15
net.ipv4.tcp_keepalive_probes = 2
net.ipv4.tcp_notsent_lowat = 131072
EOF
    else
        cat > "$TUNE_SYSCTL" <<EOF
# relay-panel TCP 调优 (standard / 标准)
net.ipv4.tcp_congestion_control = $CC
net.core.default_qdisc = fq
net.ipv4.tcp_fastopen = 3
net.core.somaxconn = 65535
net.ipv4.tcp_max_syn_backlog = 65535
net.core.netdev_max_backlog = 250000
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
net.core.rmem_default = 1048576
net.core.wmem_default = 1048576
net.ipv4.tcp_rmem = 4096 1048576 134217728
net.ipv4.tcp_wmem = 4096 1048576 134217728
net.ipv4.udp_rmem_min = 8192
net.ipv4.udp_wmem_min = 8192
net.ipv4.tcp_fin_timeout = 10
net.ipv4.tcp_mtu_probing = 1
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_no_metrics_save = 1
net.ipv4.tcp_retries2 = 8
net.ipv4.tcp_timestamps = 1
net.ipv4.tcp_sack = 1
net.ipv4.tcp_syncookies = 1
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_keepalive_time = 300
net.ipv4.tcp_keepalive_intvl = 15
net.ipv4.tcp_keepalive_probes = 2
EOF
    fi

    sysctl --system >/dev/null 2>&1 || echo "警告：sysctl --system 有报错，可手动检查 $TUNE_SYSCTL"
    echo "调优完成：$(sysctl -n net.ipv4.tcp_congestion_control) / $(sysctl -n net.core.default_qdisc)"
else
    echo "已跳过 BBR + TCP 调优（PANEL_SKIP_TUNE=1）"
fi

# ---------- 输出 ----------
if [ "$PANEL_SLAVE" = "1" ]; then
    echo ""
    echo "=========================================="
    echo " 被控节点安装完成！"
    echo "=========================================="
    echo "主控地址 : $PANEL_MASTER_URL"
    echo "服务状态 : $(systemctl is-active relay-panel-slave)"
    echo "节点会在数秒内上线（主控「节点管理」页可见）。"
    echo "日志     : journalctl -u relay-panel-slave -f"
    echo "防火墙放行各转发监听端口即可（节点无需额外入站管理端口）。"
    echo "=========================================="
    exit 0
fi

PANEL_HOST="$(curl -s4 -m 3 ifconfig.me 2>/dev/null || curl -s -m 3 ifconfig.me 2>/dev/null || echo 服务器IP)"
SCHEME="http"
FP=""
if [ "$PANEL_TLS" != "0" ] && [ -f "$STATE_DIR/tls/fingerprint" ]; then
    SCHEME="https"
    FP="$(cat "$STATE_DIR/tls/fingerprint")"
fi

echo ""
echo "=========================================="
echo " 安装完成！"
echo "=========================================="
echo "面板地址 : ${SCHEME}://${PANEL_HOST}:${PANEL_PORT}"
if [ -n "$FP" ]; then
    echo "TLS      : 已启用（自签证书）"
    echo "证书指纹 : $FP"
    echo "            被控机安装命令会自动携带该指纹（节点管理页复制）"
fi
echo "初始账号 : admin"
if [ -f "$STATE_DIR/initial-admin-password" ]; then
    echo "初始密码 : $(cat "$STATE_DIR/initial-admin-password" | awk '{print $NF}')"
    echo "          （来源 $STATE_DIR/initial-admin-password，登录后请修改密码并删除该文件）"
elif [ -n "${PANEL_ADMIN_PASSWORD:-}" ]; then
    echo "初始密码 : 你设置的 PANEL_ADMIN_PASSWORD"
else
    echo "初始密码 : 见 journalctl -u relay-panel | grep 初始密码"
fi
echo ""
echo "已有转发会在面板首次启动时自动从 /etc/tcp_pool/relays.conf 导入。"
echo "防火墙放行（如有 ufw）：ufw allow ${PANEL_PORT}/tcp 以及各转发监听端口"
echo "=========================================="
