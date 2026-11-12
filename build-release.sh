#!/bin/bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$repo_dir"
out_dir="${1:-dist}"
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"

command -v go >/dev/null 2>&1 || { echo "需要 Go 1.24+" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "需要 npm 构建当前前端" >&2; exit 1; }
(cd panel/frontend && npm ci --no-audit --no-fund && npm run typecheck && npm test && npm run build)

for arch in amd64 arm64 armv7; do
    goarch="$arch"
    [ "$arch" != armv7 ] || goarch=arm
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM=7 go build -C panel -trimpath -ldflags="-s -w" -o "$out_dir/relay-panel-linux-$arch" .
done

# Build Linux cores with the matching compiler, never the host compiler on macOS.
if [ "$(uname -s)" = Linux ]; then
    case "$(uname -m)" in
        x86_64|amd64) native_arch=amd64 ;;
        aarch64|arm64) native_arch=arm64 ;;
        armv7l|armv7*) native_arch=armv7 ;;
        *) native_arch=unknown ;;
    esac
    if [ "$native_arch" != unknown ]; then
        gcc -O2 -pthread -Wall -Wextra -Wshadow -Wformat=2 -o "$out_dir/tcp_pool-linux-$native_arch" tcp_pool.c
    fi
    for target in 'aarch64-linux-gnu-gcc arm64' 'arm-linux-gnueabihf-gcc armv7'; do
        read -r cc arch <<< "$target"
        if command -v "$cc" >/dev/null 2>&1; then
            "$cc" -O2 -pthread -Wall -Wextra -Wshadow -Wformat=2 -o "$out_dir/tcp_pool-linux-$arch" tcp_pool.c
        fi
    done
else
    echo "当前主机非 Linux，仅生成 Go 面板；静态内核请运行 build-client.sh。" >&2
fi

# The installer verifies both the panel source snapshot and the binary contents.
if command -v sha256sum >/dev/null 2>&1; then
    checksum() { sha256sum "$@"; }
else
    checksum() { shasum -a 256 "$@"; }
fi
{ find panel -maxdepth 1 -type f \( -name '*.go' -o -name go.mod -o -name go.sum \); find panel/web -type f; } |
    LC_ALL=C sort | while IFS= read -r source; do checksum "$source"; done > "$out_dir/panel-sources.sha256"
for arch in amd64 arm64 armv7; do
    binary="relay-panel-linux-$arch"
    sum="$(checksum "$out_dir/$binary")"
    printf '%s  dist/%s\n' "${sum%% *}" "$binary" > "$out_dir/$binary.sha256"
    chmod +x "$out_dir/$binary"
done
echo "产物目录：$out_dir"
