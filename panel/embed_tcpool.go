//go:build !embed_tcpool

package main

// 未加 -tags embed_tcpool 时：客户端不内嵌 tcp_pool，使用系统已安装的二进制。
var embeddedTCPPool []byte

func tcpoolEmbedded() bool { return false }
