#!/usr/bin/env python3
"""Linux-only integration: real TCP, splice, epoll and /proc CPU accounting.

Run on an idle test machine: python3 tests/test_linux.py [path/to/tcp_pool.c]
Uses loopback ephemeral ports. Does not restart or alter an installed service.
"""
import concurrent.futures
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import sys
import tempfile
import threading
import time

if sys.platform != "linux":
    print("SKIP: real epoll/splice tests require Linux")
    sys.exit(0)

SOURCE = (Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[1] / "tcp_pool.c").resolve()
WRAPPER = r'''
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdarg.h>
#include <errno.h>
/* Fault injection: emulate pipe growth refused by resource limits. */
static int small_pipe_fcntl(int fd, int cmd, ...) {
    if (cmd == F_GETFL) return fcntl(fd, cmd);
    if (cmd == F_SETPIPE_SZ) return fcntl(fd, cmd, 4096);
    if (cmd == F_SETFL) {
        va_list ap; va_start(ap, cmd); int value = va_arg(ap, int); va_end(ap);
        return fcntl(fd, cmd, value);
    }
    errno = EINVAL;
    return -1;
}
#define fcntl small_pipe_fcntl
#include RELAY_SOURCE
'''


def listener():
    sock = socket.socket()
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    sock.bind(("127.0.0.1", 0))
    sock.listen()
    sock.settimeout(15)
    return sock


def read_all(sock):
    parts = []
    while True:
        chunk = sock.recv(65536)
        if not chunk:
            return b"".join(parts)
        parts.append(chunk)


def cpu_seconds(pid):
    # comm may contain spaces, so parse after the final ')'.
    fields = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    return (int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK")


def assert_idle(process, label):
    time.sleep(0.25)
    before = cpu_seconds(process.pid)
    time.sleep(0.75)
    consumed = cpu_seconds(process.pid) - before
    print(f"{label}: {consumed:.3f}s CPU / 0.75s wall", flush=True)
    assert consumed < 0.15, f"{label}: relay busy-spins"
    assert process.poll() is None


def start_relay(binary, upstream, **overrides):
    with listener() as reservation:
        port = reservation.getsockname()[1]
    env = os.environ.copy()
    env.update(LOCAL_IP="127.0.0.1", LOCAL_PORT=str(port), REMOTE_IP="127.0.0.1",
               REMOTE_TCP_PORT=str(upstream), REMOTE_UDP_PORT=str(upstream),
               POOL_SIZE="0", LOG_ENABLE="0", HALF_CLOSE_TIMEOUT="30",
               SPLICE_CHUNK=str(256 * 1024), ENABLE_TCP="1", ENABLE_UDP="1",
               RATE_LIMIT_KBPS="0", STATS_INTERVAL="0")
    env.update(overrides)
    process = subprocess.Popen([str(binary)], env=env, stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, text=True)
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try:
            client = socket.create_connection(("127.0.0.1", port), timeout=0.2)
            client.settimeout(15)
            return process, client
        except ConnectionRefusedError:
            if process.poll() is not None:
                raise RuntimeError(process.stderr.read())
            time.sleep(0.02)
    process.terminate()
    process.communicate(timeout=5)
    raise TimeoutError("relay did not listen")


def stop(process):
    process.terminate()
    try:
        _, stderr = process.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        _, stderr = process.communicate(timeout=5)
    if stderr:
        print(stderr, file=sys.stderr)


def connected_pair(binary, upstream):
    process, client = start_relay(binary, upstream.getsockname()[1])
    try:
        remote, _ = upstream.accept()
        remote.settimeout(15)
        return process, client, remote
    except BaseException:
        client.close()
        stop(process)
        raise


def tcp_autotune_limit(name):
    # Third field of tcp_rmem/tcp_wmem: how far the kernel may grow one socket buffer.
    try:
        return int(Path(f"/proc/sys/net/ipv4/{name}").read_text().split()[2])
    except (OSError, ValueError, IndexError):
        return 8 * 1024 * 1024


def backpressure(binary, reverse):
    # The relay's two sockets autotune, so together they can hold up to
    # tcp_rmem max + tcp_wmem max. A fixed payload that fits in there is sent
    # in full without ever blocking, and the test proves nothing. The test's
    # own sockets are pinned small; the payload is sized past the relay's share.
    slack = 4 * 1024 * 1024
    size = max(8 * 1024 * 1024, tcp_autotune_limit("tcp_rmem") + tcp_autotune_limit("tcp_wmem") + slack)
    payload = bytes(range(256)) * (size // 256 + 1)
    with listener() as upstream:
        process, client, remote = connected_pair(binary, upstream)
        sender, receiver = (remote, client) if reverse else (client, remote)
        sender.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, 32768)
        receiver.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 32768)
        release = threading.Event()
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                def send():
                    sender.sendall(payload)
                    sender.shutdown(socket.SHUT_WR)

                def receive():
                    if not release.wait(10):
                        raise TimeoutError("receiver was not released")
                    return read_all(receiver)

                writing = pool.submit(send)
                reading = pool.submit(receive)
                try:
                    time.sleep(0.5)
                    assert not writing.done(), "test did not establish backpressure"
                    assert_idle(process, "remote->local blocked" if reverse else "local->remote blocked")
                finally:
                    release.set()
                writing.result(timeout=15)
                assert reading.result(timeout=15) == payload, "payload lost/corrupted"
                # Confirm the opposite direction survives the first FIN.
                receiver.sendall(b"reply-after-fin")
                receiver.shutdown(socket.SHUT_WR)
                assert read_all(sender) == b"reply-after-fin"
            print("PASS backpressure", "reverse" if reverse else "forward", flush=True)
        finally:
            release.set()
            client.close()
            remote.close()
            stop(process)


def half_close(binary):
    with listener() as upstream:
        process, client, remote = connected_pair(binary, upstream)
        try:
            client.sendall(b"request")
            client.shutdown(socket.SHUT_WR)
            assert read_all(remote) == b"request"
            assert_idle(process, "half-close idle")
            remote.sendall(b"delayed response")
            remote.shutdown(socket.SHUT_WR)
            assert read_all(client) == b"delayed response"
            print("PASS half_close", flush=True)
        finally:
            client.close()
            remote.close()
            stop(process)



def udp_socket():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("127.0.0.1", 0))
    sock.settimeout(5)
    return sock


def udp_relay(binary, upstream, stats, tcp_port=0, rate=0):
    # Reserve both protocols while selecting a port; release immediately before exec.
    with listener() as reservation:
        port = reservation.getsockname()[1]
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
            udp.bind(("127.0.0.1", port))
    env = os.environ.copy()
    env.update(LOCAL_IP="127.0.0.1", LOCAL_PORT=str(port), REMOTE_IP="127.0.0.1",
               REMOTE_TCP_PORT=str(tcp_port), REMOTE_UDP_PORT=str(upstream),
               ENABLE_TCP="1" if tcp_port else "0", ENABLE_UDP="1", POOL_SIZE="0",
               RATE_LIMIT_KBPS=str(rate), STATS_FILE=str(stats), STATS_INTERVAL="1",
               LOG_ENABLE="0")
    process = subprocess.Popen([str(binary)], env=env, stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, text=True)
    deadline = time.monotonic() + 5
    while not stats.exists():
        if process.poll() is not None:
            raise RuntimeError(process.stderr.read())
        if time.monotonic() >= deadline:
            stop(process)
            raise TimeoutError("UDP relay did not initialize")
        time.sleep(0.02)
    return process, ("127.0.0.1", port)


def await_stats(path, up, down):
    deadline = time.monotonic() + 4
    while time.monotonic() < deadline:
        stats = json.loads(path.read_text())
        if stats["up"] == up and stats["down"] == down:
            return stats
        time.sleep(0.05)
    raise AssertionError(f"wrong forwarded-byte statistics: {stats}; expected {up}/{down}")


def recv_exact(sock, size):
    result = bytearray()
    while len(result) < size:
        data = sock.recv(size - len(result))
        if not data:
            raise AssertionError("unexpected TCP EOF")
        result.extend(data)
    return bytes(result)


def udp_forwarding(binary, mixed):
    with tempfile.TemporaryDirectory(prefix="udp-stats-") as tmp:
        stats_file = Path(tmp) / "stats.json"
        with udp_socket() as upstream, listener() as tcp_upstream:
            process, address = udp_relay(binary, upstream.getsockname()[1], stats_file,
                                         tcp_upstream.getsockname()[1] if mixed else 0)
            try:
                up = down = 0
                if not mixed:
                    # UDP-only must leave the TCP port free, with no required TCP target.
                    with socket.socket() as unused_tcp:
                        unused_tcp.bind(address)
                else:
                    with socket.create_connection(address, timeout=5) as client:
                        remote, _ = tcp_upstream.accept()
                        with remote:
                            client.settimeout(5)
                            remote.settimeout(5)
                            client.sendall(b"tcp-request")
                            assert recv_exact(remote, 11) == b"tcp-request"
                            remote.sendall(b"tcp-response")
                            assert recv_exact(client, 12) == b"tcp-response"
                            up += 11
                            down += 12
                # Multiple clients must retain their own reverse path and datagram boundaries.
                with udp_socket() as first, udp_socket() as second:
                    for client in (first, second):
                        for payload in (b"", b"x", b"u" * 1500, b"v" * 60000):
                            client.sendto(payload, address)
                            received, source = upstream.recvfrom(65535)
                            assert received == payload, "UDP request was split or corrupted"
                            reply = b"reply:" + payload
                            upstream.sendto(reply, source)
                            received, sender = client.recvfrom(65535)
                            assert received == reply and sender == address
                            up += len(payload)
                            down += len(reply)
                stats = await_stats(stats_file, up, down)
                assert stats["udp_total"] == 2
                if not mixed:
                    assert stats["conns_total"] == 0
                assert_idle(process, "UDP idle")
                print("PASS udp_forwarding", "TCP+UDP" if mixed else "UDP-only", flush=True)
            finally:
                stop(process)


def udp_rate_limit(binary):
    rate = 8 * 1024
    payload = b"x" * 1024
    with tempfile.TemporaryDirectory(prefix="udp-rate-") as tmp:
        stats_file = Path(tmp) / "stats.json"
        with udp_socket() as upstream, udp_socket() as client:
            process, address = udp_relay(binary, upstream.getsockname()[1], stats_file, rate=8)
            try:
                def flood(sender, target, receiver):
                    receiver.settimeout(0.1)
                    deadline = time.monotonic() + 1.5
                    started = time.monotonic()
                    received = []
                    peers = []
                    done = threading.Event()
                    def collect():
                        while not done.is_set() or time.monotonic() < deadline + 0.2:
                            try:
                                data, peer = receiver.recvfrom(65535)
                                assert data == payload
                                received.append(len(data))
                                peers.append(peer)
                            except socket.timeout:
                                continue
                    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                        reading = pool.submit(collect)
                        try:
                            while time.monotonic() < deadline:
                                sender.sendto(payload, target)
                                time.sleep(0.001)
                        finally:
                            done.set()
                        reading.result(timeout=5)
                    duration = time.monotonic() - started
                    count = sum(received)
                    # One-second initial burst plus one whole datagram of debt.
                    assert 0 < count <= rate * (duration + 1) + len(payload), (count, duration)
                    assert len(received) < 100, "UDP rate limit did not drop excess packets"
                    return count, peers[-1]

                up, association = flood(client, address, upstream)
                down, _ = flood(upstream, association, client)
                await_stats(stats_file, up, down)
                print("PASS udp_rate_limit both directions", up, down, flush=True)
            finally:
                stop(process)


def udp_large_low_rate(binary):
    with tempfile.TemporaryDirectory(prefix="udp-large-") as tmp:
        with udp_socket() as upstream, udp_socket() as client:
            process, address = udp_relay(binary, upstream.getsockname()[1], Path(tmp)/"stats.json", rate=1)
            try:
                payload = b"large-datagram" * 120  # More than the 1 KiB bucket capacity.
                client.sendto(payload, address)
                assert upstream.recvfrom(65535)[0] == payload
                # Wait for the debt and one more positive byte; a larger datagram must not
                # be rejected forever simply because it exceeds the configured burst.
                time.sleep(1.8)
                client.sendto(payload, address)
                assert upstream.recvfrom(65535)[0] == payload
                print("PASS udp_large_low_rate", flush=True)
            finally:
                stop(process)


def tcp_rate_limit(binary):
    with listener() as upstream:
        process, client = start_relay(binary, upstream.getsockname()[1], RATE_LIMIT_KBPS="16", ENABLE_UDP="0", REMOTE_UDP_PORT="0")
        remote, _ = upstream.accept()
        try:
            remote.settimeout(10)
            client.settimeout(10)
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as unused_udp:
                unused_udp.bind(client.getpeername())  # TCP-only leaves UDP unbound.
            payload = b"x" * (48 * 1024)
            for sender, receiver in ((client, remote), (remote, client)):
                started = time.monotonic()
                sender.sendall(payload)
                received = bytearray()
                while len(received) < len(payload):
                    chunk = receiver.recv(len(payload)-len(received))
                    assert chunk, "unexpected TCP EOF while rate limited"
                    received.extend(chunk)
                elapsed = time.monotonic()-started
                assert bytes(received) == payload
                assert elapsed >= 1.7, f"TCP rate limit bypassed: {elapsed} seconds"
            print("PASS tcp_rate_limit both directions", flush=True)
        finally:
            client.close()
            remote.close()
            stop(process)

with tempfile.TemporaryDirectory(prefix="tcp-pool-linux-") as tmp:
    wrapper = Path(tmp) / "small_pipe.c"
    binary = Path(tmp) / "tcp_pool"
    wrapper.write_text(WRAPPER)
    subprocess.run(shlex.split(os.environ.get("CC", "cc")) + [
        "-O2", "-pthread", "-Wall", "-Wextra", "-Wshadow", "-Wformat=2",
        f'-DRELAY_SOURCE="{SOURCE}"', str(wrapper), "-o", str(binary),
    ], check=True)
    backpressure(binary, reverse=False)
    backpressure(binary, reverse=True)
    half_close(binary)
    tcp_rate_limit(binary)
    udp_forwarding(binary, mixed=False)
    udp_forwarding(binary, mixed=True)
    udp_rate_limit(binary)
    udp_large_low_rate(binary)
