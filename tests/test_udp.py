#!/usr/bin/env python3
"""Portable UDP regression using the real forwarding handlers and loopback sockets.

Only epoll registration is stubbed; UDP send/receive, client associations, packet
boundaries, rate budgets and statistics run through the production C functions.
"""
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile

source_path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[1] / "tcp_pool.c"
source = source_path.read_text()
rate = source[source.index("//流量统计与实例级限速"):source.index("static void conn_close")]
udp = source[source.index("//UDP转发"):source.index("//把统计快照写到")]

PREAMBLE = r'''
#include <arpa/inet.h>
#include <assert.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <unistd.h>
#define UDP_TABLE_SIZE 1024
#define EPOLLIN 1U
#define EPOLLET (1U << 31)
#define EPOLL_CTL_ADD 1
#define EPOLL_CTL_DEL 2
#define TAG_UDP_ASSOC ((uintptr_t)2)
struct epoll_event { uint32_t events; union { void *ptr; } data; };
static struct sockaddr_storage remote_udp_addr;
static socklen_t remote_udp_addrlen;
static int epfd;
static int epoll_ctl(int ep, int op, int fd, struct epoll_event *ev) {
    (void)ep; (void)fd;
    if (op == EPOLL_CTL_ADD) assert(ev->events == EPOLLIN); // Bounded handler needs LT.
    return 0;
}
static int set_nonblock(int fd) {
    return fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK);
}
static void set_udp_socket_options(int fd) {
    int size = 1024 * 1024;
    assert(setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &size, sizeof(size)) == 0);
    assert(setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &size, sizeof(size)) == 0);
}
'''

TESTS = r'''
static int bound_socket(struct sockaddr_storage *address, socklen_t *length) {
    int fd = socket(AF_INET, SOCK_DGRAM, 0);
    assert(fd >= 0);
    struct sockaddr_in addr = {0};
    addr.sin_family = AF_INET;
    addr.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    assert(bind(fd, (struct sockaddr *)&addr, sizeof(addr)) == 0);
    *length = sizeof(*address);
    assert(getsockname(fd, (struct sockaddr *)address, length) == 0);
    set_udp_socket_options(fd);
    assert(set_nonblock(fd) == 0);
    return fd;
}
static void readable(int fd) {
    struct pollfd p = {fd, POLLIN, 0};
    assert(poll(&p, 1, 1000) == 1 && (p.revents & POLLIN));
}
static void empty(int fd) {
    char b[1];
    assert(recv(fd, b, sizeof(b), 0) == -1);
    assert(errno == EAGAIN || errno == EWOULDBLOCK);
}
static UdpAssoc *association(const struct sockaddr_storage *address) {
    UdpAssoc *u = udp_tab[udp_hash_addr(address)];
    while (u && !udp_addr_eq(&u->cli, address)) u = u->next;
    assert(u);
    return u;
}
static void receive_payload(int fd, const void *expected, size_t length,
                            struct sockaddr_storage *peer, socklen_t *peer_len) {
    char data[65535];
    readable(fd);
    assert(recvfrom(fd, data, sizeof(data), 0, (struct sockaddr *)peer, peer_len) == (ssize_t)length);
    assert(memcmp(data, expected, length) == 0);
}
int main(void) {
    // The rate section also declares TCP counters/pump state, unused by this harness.
    (void)g_pump_tokens; (void)g_pump_bytes; (void)g_conns_active;
    struct sockaddr_storage local, client_addr[2], peers[2];
    socklen_t local_len, lengths[2], peer_lengths[2];
    int remote = bound_socket(&remote_udp_addr, &remote_udp_addrlen);
    udp_listen_fd = bound_socket(&local, &local_len);
    int clients[2];
    for (int i = 0; i < 2; i++) clients[i] = bound_socket(&client_addr[i], &lengths[i]);
    g_rate_kbps = 0;
    rate_init(0);
    char data[60000];
    for (size_t i = 0; i < sizeof(data); i++) data[i] = (char)(i % 251);
    size_t sizes[] = {0, 1, 1500, sizeof(data)};
    uint64_t total = 0;
    for (int i = 0; i < 2; i++) {
        for (size_t j = 0; j < sizeof(sizes)/sizeof(sizes[0]); j++) {
            size_t length = sizes[j];
            assert(sendto(clients[i], data, length, 0, (struct sockaddr *)&local, local_len) == (ssize_t)length);
            readable(udp_listen_fd);
            udp_receive_clients(0);
            peer_lengths[i] = sizeof(peers[i]);
            receive_payload(remote, data, length, &peers[i], &peer_lengths[i]);
            assert(sendto(remote, data, length, 0, (struct sockaddr *)&peers[i], peer_lengths[i]) == (ssize_t)length);
            UdpAssoc *u = association(&client_addr[i]);
            readable(u->up_fd);
            udp_receive_remote(u, 0);
            struct sockaddr_storage from; socklen_t from_len = sizeof(from);
            receive_payload(clients[i], data, length, &from, &from_len);
            assert(udp_addr_eq(&from, &local));
            empty(clients[1-i]);
            total += length;
        }
    }
    assert(g_udp_bytes_up == total && g_udp_bytes_down == total);
    assert(g_udp_total == 2 && g_udp_sessions == 2);
    assert(!udp_addr_eq(&peers[0], &peers[1]));
    puts("PASS UDP unlimited, empty/large datagrams, client isolation and statistics");

    // Limit both directions; one complete datagram may exceed the small burst.
    g_rate_kbps = 1;
    rate_init(0);
    for (int i = 0; i < 2; i++) {
        assert(sendto(clients[0], data, 1500, 0, (struct sockaddr *)&local, local_len) == 1500);
        readable(udp_listen_fd);
        udp_receive_clients(0);
        if (!i) receive_payload(remote, data, 1500, NULL, NULL);
        else empty(remote);
        assert(sendto(remote, data, 1500, 0, (struct sockaddr *)&peers[0], peer_lengths[0]) == 1500);
        UdpAssoc *u = association(&client_addr[0]);
        readable(u->up_fd);
        udp_receive_remote(u, 0);
        if (!i) receive_payload(clients[0], data, 1500, NULL, NULL);
        else empty(clients[0]);
    }
    assert(g_udp_bytes_up == total+1500 && g_udp_bytes_down == total+1500);
    assert(g_tokens_up == -476 && g_tokens_down == -476);
    assert(rate_refill(1000));
    assert(sendto(clients[0], data, 1500, 0, (struct sockaddr *)&local, local_len) == 1500);
    readable(udp_listen_fd);
    udp_receive_clients(1000);
    receive_payload(remote, data, 1500, NULL, NULL);
    assert(g_udp_bytes_up == total+3000);
    puts("PASS UDP bidirectional limit, whole-packet dropping and refill");

    // More than one handler budget stays ready instead of monopolizing the loop.
    g_rate_kbps = 0;
    uint64_t before_batch = g_udp_bytes_up;
    for (int i = 0; i < 65; i++) {
        assert(sendto(clients[0], data, 10, 0, (struct sockaddr *)&local, local_len) == 10);
    }
    readable(udp_listen_fd);
    udp_receive_clients(1500);
    assert(g_udp_bytes_up == before_batch + 64 * 10);
    for (int i = 0; i < 64; i++) receive_payload(remote, data, 10, NULL, NULL);
    readable(udp_listen_fd);
    udp_receive_clients(1500);
    receive_payload(remote, data, 10, NULL, NULL);
    assert(g_udp_bytes_up == before_batch + 65 * 10);
    puts("PASS UDP handler yields after bounded packet batch");

    // Failed forwarding must not count bytes or consume the budget.
    g_rate_kbps = 0;
    UdpAssoc *u = association(&client_addr[0]);
    close(u->up_fd); u->up_fd = -1;
    uint64_t before = g_udp_bytes_up;
    assert(sendto(clients[0], data, 100, 0, (struct sockaddr *)&local, local_len) == 100);
    readable(udp_listen_fd);
    udp_receive_clients(2000);
    assert(g_udp_bytes_up == before);
    puts("PASS UDP failed send excluded from forwarded statistics");

    for (uint32_t i = 0; i < UDP_TABLE_SIZE; i++) {
        while (udp_tab[i]) udp_remove(udp_tab[i], i, epfd);
    }
    assert(g_udp_sessions == 0);
    close(clients[0]); close(clients[1]); close(remote); close(udp_listen_fd);
    return 0;
}
'''

with tempfile.TemporaryDirectory(prefix="tcp-pool-udp-") as tmp:
    src, binary = Path(tmp)/"udp.c", Path(tmp)/"udp"
    src.write_text(PREAMBLE + rate + udp + TESTS)
    subprocess.run(shlex.split(os.environ.get("CC", "cc")) + [
        "-std=c11", "-Wall", "-Wextra", "-Werror", "-O1",
        "-fsanitize=address,undefined", str(src), "-o", str(binary),
    ], check=True)
    subprocess.run([str(binary)], check=True, timeout=15)
