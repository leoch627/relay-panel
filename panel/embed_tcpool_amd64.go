//go:build embed_tcpool && amd64

package main

import _ "embed"

// 单文件客户端：内嵌 musl 静态版 tcp_pool，首次运行自解压到 /root/tcp_pool。
// musl 静态链接保证在 Alpine 与 glibc 发行版上都能直接运行。
//
//go:embed bin/tcp_pool-linux-amd64-musl
var embeddedTCPPool []byte

func tcpoolEmbedded() bool { return true }
