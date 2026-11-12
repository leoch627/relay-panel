#!/usr/bin/env python3
"""Exercise installer helpers with temporary paths and a fake systemctl."""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


INSTALLER = Path(__file__).resolve().parents[1] / "panel_install.sh"
SOURCE = INSTALLER.read_text()
HELPERS = "\n".join(
    re.search(rf"(?ms)^{name}\(\) \{{\n.*?^\}}\n", SOURCE).group(0)
    for name in (
        "tcp_pool_has_features",
        "install_binary_atomic",
        "snapshot_running_tcp_pool_units",
        "restart_updated_tcp_pool_units",
        "install_tcp_pool",
    )
)

PRELUDE = r'''
set -euo pipefail
PANEL_SLAVE=0
TCPPOOL_UPDATED=0
TCPPOOL_ACTIVE_UNITS=()
TCPPool_BIN="$TEST_DIR/tcp_pool"
SRC_DIR="$TEST_DIR/source"
ARCH=amd64
mkdir -p "$SRC_DIR/dist"
systemctl() {
    printf '%s\n' "$*" >> "$TEST_DIR/systemctl.log"
    case "$1" in
        list-units)
            [ "${LIST_FAIL:-0}" = 0 ] || return 1
            printf '%s\n' "${RUNNING_UNITS:-}"
            ;;
        try-restart)
            shift
            for unit in "$@"; do
                if [ -e "$TEST_DIR/running.$unit" ]; then
                    printf '%s\n' "$unit" >> "$TEST_DIR/restarted"
                fi
            done
            ;;
        *) return 99 ;;
    esac
}
install_build_deps() { :; }
'''


class PanelInstallTests(unittest.TestCase):
    def run_helpers(self, script):
        with tempfile.TemporaryDirectory(prefix="relay-installer-test-") as directory:
            result = subprocess.run(
                ["bash", "-c", PRELUDE + HELPERS + script],
                env={**os.environ, "TEST_DIR": directory},
                text=True,
                capture_output=True,
                timeout=10,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_restart_only_originally_running_and_still_running(self):
        self.run_helpers(r'''
RUNNING_UNITS=$'tcp-pool@active.service loaded active running Active\ntcp-pool@stopped-during.service loaded active running Stop during install'
touch "$TEST_DIR/running.tcp-pool@active.service"
touch "$TEST_DIR/running.tcp-pool@stopped-during.service"
snapshot_running_tcp_pool_units
# Simulate a quota/user stop, and an unrelated new rule started during upgrade.
rm "$TEST_DIR/running.tcp-pool@stopped-during.service"
touch "$TEST_DIR/running.tcp-pool@new.service"
TCPPOOL_UPDATED=1
restart_updated_tcp_pool_units
[ "$(cat "$TEST_DIR/restarted")" = 'tcp-pool@active.service' ]
grep -qx 'try-restart tcp-pool@active.service tcp-pool@stopped-during.service' "$TEST_DIR/systemctl.log"
''')

    def test_no_restart_without_replacement_or_running_rules(self):
        self.run_helpers(r'''
RUNNING_UNITS='tcp-pool@active.service loaded active running Active'
snapshot_running_tcp_pool_units
restart_updated_tcp_pool_units
! grep -q '^try-restart' "$TEST_DIR/systemctl.log"
RUNNING_UNITS=''
snapshot_running_tcp_pool_units
TCPPOOL_UPDATED=1
restart_updated_tcp_pool_units
! grep -q '^try-restart' "$TEST_DIR/systemctl.log"
''')

    def test_slave_delegates_restart_to_agent(self):
        self.run_helpers(r'''
PANEL_SLAVE=1
TCPPOOL_UPDATED=1
snapshot_running_tcp_pool_units
restart_updated_tcp_pool_units
[ ! -e "$TEST_DIR/systemctl.log" ]
''')

    def test_failed_snapshot_does_not_replace_kernel(self):
        self.run_helpers(r'''
LIST_FAIL=1
gcc() { touch "$TEST_DIR/compiler-called"; }
set +e
(set -e; install_tcp_pool)
status=$?
set -e
[ "$status" != 0 ]
[ ! -e "$TEST_DIR/compiler-called" ]
[ ! -e "$TCPPool_BIN" ]
''')

    def test_atomic_replacement_preserves_previous_inode(self):
        self.run_helpers(r'''
printf old > "$TCPPool_BIN"
ln "$TCPPool_BIN" "$TEST_DIR/previous-inode"
printf new > "$TEST_DIR/new-binary"
install_binary_atomic "$TEST_DIR/new-binary" "$TCPPool_BIN"
[ "$(cat "$TCPPool_BIN")" = new ]
[ "$(cat "$TEST_DIR/previous-inode")" = old ]
[ -x "$TCPPool_BIN" ]
''')

    def test_failed_atomic_copy_keeps_previous_binary_and_removes_temp(self):
        self.run_helpers(r'''
printf old > "$TCPPool_BIN"
install() { return 1; }
if install_binary_atomic "$TEST_DIR/missing" "$TCPPool_BIN"; then exit 1; fi
[ "$(cat "$TCPPool_BIN")" = old ]
shopt -s nullglob
leftovers=("$TCPPool_BIN".install.*)
[ "${#leftovers[@]}" = 0 ]
''')

    def test_prebuilt_fallback_marks_replacement_and_is_atomic(self):
        self.run_helpers(r'''
printf old > "$TCPPool_BIN"
ln "$TCPPool_BIN" "$TEST_DIR/previous-inode"
printf '#!/bin/sh\necho udp_rate_limit\n' > "$SRC_DIR/dist/tcp_pool-linux-amd64"
chmod +x "$SRC_DIR/dist/tcp_pool-linux-amd64"
gcc() { return 1; }
install_tcp_pool
[ "$TCPPOOL_UPDATED" = 1 ]
[ "$(cat "$TEST_DIR/previous-inode")" = old ]
tcp_pool_has_features "$TCPPool_BIN"
''')

    def test_successful_build_marks_replacement(self):
        self.run_helpers(r'''
gcc() {
    while [ "$1" != -o ]; do shift; done
    printf '#!/bin/sh\necho udp_rate_limit\n' > "$2"
}
install_tcp_pool
[ "$TCPPOOL_UPDATED" = 1 ]
tcp_pool_has_features "$TCPPool_BIN"
''')


if __name__ == "__main__":
    unittest.main()
