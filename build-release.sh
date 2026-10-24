#!/bin/bash
set -euo pipefail

out_dir="${1:-dist}"
mkdir -p "$out_dir"

# ---------- 前端（React + shadcn/ui，产物输出 panel/web） ----------
if command -v npm >/dev/null 2>&1 && [ -f panel/frontend/package.json ]; then
    echo "==> 构建前端 ..."
    (cd panel/frontend && { [ -d node_modules ] || npm install --no-audit --no-fund; } && npm run build)
else
    echo "!! 跳过前端构建（无 npm），沿用已提交的 panel/web 产物" >&2
fi

# ---------- 面板（Go，全部架构交叉编译，无 CGO 依赖） ----------
if command -v go >/dev/null 2>&1 && [ -f panel/main.go ]; then
    echo "==> 构建 relay-panel ..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -C panel -trimpath -ldflags="-s -w" -o "../$out_dir/relay-panel-linux-amd64" .
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -C panel -trimpath -ldflags="-s -w" -o "../$out_dir/relay-panel-linux-arm64" .
    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -C panel -trimpath -ldflags="-s -w" -o "../$out_dir/relay-panel-linux-armv7" .
    chmod +x "$out_dir"/relay-panel-linux-*
else
    echo "!! 跳过 relay-panel（需要 go 与 panel/ 目录）" >&2
fi

# ---------- 转发核心 tcp_pool（C，本机架构 + 可选交叉编译） ----------
build_tcp_pool() {
    local asset="$1"; shift
    gcc "$@" -Wall -Wextra -Wshadow -Wformat=2 -o "$out_dir/$asset" tcp_pool.c
    chmod +x "$out_dir/$asset"
    file "$out_dir/$asset"
}

case "$(uname -m)" in
    x86_64|amd64)
        build_tcp_pool tcp_pool-linux-amd64 -O2 -pthread -march=x86-64 -mtune=generic
        command -v aarch64-linux-gnu-gcc >/dev/null 2>&1 && \
            build_tcp_pool tcp_pool-linux-arm64 -O2 -pthread
        command -v arm-linux-gnueabihf-gcc >/dev/null 2>&1 && \
            build_tcp_pool tcp_pool-linux-armv7 -O2 -pthread -march=armv7-a -mfpu=vfpv3-d16 -mfloat-abi=hard
        ;;
    aarch64|arm64)
        build_tcp_pool tcp_pool-linux-arm64 -O2 -pthread
        ;;
    armv7l|armv7*)
        build_tcp_pool tcp_pool-linux-armv7 -O2 -pthread -march=armv7-a -mfpu=vfpv3-d16 -mfloat-abi=hard
        ;;
    i386|i686)
        build_tcp_pool tcp_pool-linux-386 -O2 -pthread -march=i686 -mtune=generic
        ;;
    *)
        echo "unsupported local architecture: $(uname -m)" >&2
        exit 1
        ;;
esac

echo "==> 产物："
ls -la "$out_dir"
