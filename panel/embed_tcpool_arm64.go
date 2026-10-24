//go:build embed_tcpool && arm64

package main

import _ "embed"

//go:embed bin/tcp_pool-linux-arm64-musl
var embeddedTCPPool []byte

func tcpoolEmbedded() bool { return true }
