//go:build embed_tcpool && !amd64 && !arm64

package main

// 单文件客户端目前仅支持 amd64 / arm64（与 CI 产物一致）。
const _embedTcpoolUnsupportedArch = "embed_tcpool 仅支持 linux/amd64 与 linux/arm64"

var _ = _embedTcpoolUnsupportedArch
