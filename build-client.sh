#!/bin/bash
# 本地构建单文件客户端（需要 zig 提供 musl 静态交叉编译；CI 用同一套命令）
# 用法：./build-client.sh [输出目录，默认 dist]
set -euo pipefail

out_dir="${1:-dist}"
mkdir -p "$out_dir" panel/bin

command -v zig >/dev/null 2>&1 || { echo "需要 zig（brew install zig / 官方安装）" >&2; exit 1; }

echo "==> musl 静态编译 tcp_pool（Alpine 兼容）..."
zig cc -target x86_64-linux-musl  -O2 -pthread -Wall -Wextra -Wshadow -Wformat=2 -o "$out_dir/tcp_pool-linux-amd64-musl" tcp_pool.c
zig cc -target aarch64-linux-musl -O2 -pthread -Wall -Wextra -Wshadow -Wformat=2 -o "$out_dir/tcp_pool-linux-arm64-musl" tcp_pool.c
cp "$out_dir/tcp_pool-linux-amd64-musl" "$out_dir/tcp_pool-linux-arm64-musl" panel/bin/

echo "==> 构建单文件客户端（Go 静态 + 内嵌 tcp_pool）..."
cd panel
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed_tcpool -trimpath -ldflags="-s -w" -o "../$out_dir/relay-panel-client-linux-amd64" .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags embed_tcpool -trimpath -ldflags="-s -w" -o "../$out_dir/relay-panel-client-linux-arm64" .
cd ..

chmod +x "$out_dir"/relay-panel-client-* "$out_dir"/*-musl
file "$out_dir"/relay-panel-client-* "$out_dir"/*-musl
echo "==> 完成。客户端用法（被控机，root）："
echo "  ./relay-panel-client-linux-\$(uname -m 对应架构) -slave -master https://主控:8081 -node-key 密钥 -tls-fingerprint 指纹 -self-install"
