"""Real-device lifecycle tests for the packaged macOS desktop shell.

Runs the actual `.app` produced by scripts/build-macos.sh for the host
architecture and checks the shell <-> Core process contract: ownership token,
parent/child relationship, health + UI over the dynamic port, fail-closed port
conflict, flock single-instance, and the crash watchdog.

Run either way:
    python3 -m unittest -v tests.macos.test_desktop_lifecycle
    python3 tests/macos/test_desktop_lifecycle.py
"""
import os
import pathlib
import platform
import signal
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]


def host_bundle():
    """Pick the bundle matching the host CPU so the test never runs an
    emulated binary (the amd64 app used to be hardcoded, which on Apple
    Silicon exercises Rosetta instead of the shipped native artifact)."""
    arch = platform.machine()
    go_arch = "arm64" if arch == "arm64" else "amd64"
    return ROOT / f"dist/RelayHub-{go_arch}.app/Contents/MacOS/RelayHub"


BINARY = host_bundle()


def get_free_port():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def read_token(data_dir, timeout=10.0):
    """Wait for the shell to record Core ownership; return (pid, token)."""
    deadline = time.monotonic() + timeout
    tfile = pathlib.Path(data_dir) / "relayhub.token"
    while time.monotonic() < deadline:
        if tfile.exists():
            pid = token = None
            for line in tfile.read_text().splitlines():
                if line.startswith("pid="):
                    pid = int(line.split("=", 1)[1])
                elif line.startswith("token="):
                    token = line.split("=", 1)[1]
            if pid:
                return pid, token
        time.sleep(0.05)
    return None, None


def wait_healthy(port, timeout=15.0):
    """Wait until the core answers /healthz. The shell records the core's PID
    as soon as it launches it, before the core has bound its port, so a
    request right after read_token() can race the listener (seen on CI as
    "Connection refused")."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1) as r:
                if r.status == 200:
                    return True
        except (urllib.error.URLError, ConnectionError, OSError):
            pass
        time.sleep(0.1)
    return False


def spawn(port, data_dir, home):
    # The core inherits this environment. Give its proxies and gateway free
    # ports instead of the fixed 8787-8789: a core from the previous test can
    # still hold those while it shuts down, and the new core then exits with
    # "address already in use" (seen on CI as a vanished core PID or a
    # refused /healthz).
    env = dict(os.environ, HOME=home, RELAYHUB_NO_ALERT_MODAL="1",
               RELAYHUB_HTTP_PROXY_ADDR=f"127.0.0.1:{get_free_port()}",
               RELAYHUB_SOCKS5_ADDR=f"127.0.0.1:{get_free_port()}",
               RELAYHUB_GATEWAY_ADDR=f"127.0.0.1:{get_free_port()}")
    return subprocess.Popen(
        [str(BINARY), "--port", str(port), "--data-dir", str(data_dir)],
        env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )


def stop(proc):
    """Terminate the shell, drain its pipes, and return combined output."""
    proc.terminate()
    try:
        out, err = proc.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        out, err = proc.communicate()
    return (out or "") + (err or "")


@unittest.skipUnless(platform.system() == "Darwin", "macOS desktop shell only runs on Darwin")
class DesktopLifecycleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not BINARY.exists():
            raise unittest.SkipTest(
                f"packaged bundle missing at {BINARY}; run ./scripts/build-macos.sh first")

    def test_full_lifecycle(self):
        port = get_free_port()
        with tempfile.TemporaryDirectory() as td:
            data_dir = pathlib.Path(td) / "data"
            data_dir.mkdir()
            proc = spawn(port, data_dir, td)
            core_pid = None
            passed = False
            try:
                core_pid, core_token = read_token(data_dir)
                self.assertIsNotNone(core_pid, "Core PID not recorded in token")
                self.assertIsNotNone(core_token, "Core UUID token not recorded in token")

                ps_ppid = subprocess.run(["ps", "-p", str(core_pid), "-o", "ppid="],
                                         capture_output=True, text=True).stdout.strip()
                self.assertEqual(int(ps_ppid), proc.pid,
                                 f"Core PPID {ps_ppid} does not match Shell PID {proc.pid}")

                self.assertTrue(wait_healthy(port), "Core never became healthy")
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=2) as r:
                    self.assertEqual(r.status, 200)
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=2) as r:
                    self.assertEqual(r.status, 200)
                    self.assertIn("text/html", r.headers.get("Content-Type", ""))
                # The management API is authenticated by default (AUDIT
                # 2026-09-24 F4): anonymous reads are refused, and the core
                # generates its token into the shell's data dir.
                summary = f"http://127.0.0.1:{port}/api/v1/usage/summary"
                with self.assertRaises(urllib.error.HTTPError) as denied:
                    urllib.request.urlopen(summary, timeout=2)
                self.assertEqual(denied.exception.code, 401)
                token = (data_dir / "management.token").read_text().strip()
                self.assertGreaterEqual(len(token), 32)
                # Management API served through the shell's port: the extras
                # family must be routed (P0-1 / RH-05 regression on real binary).
                authed = urllib.request.Request(summary, headers={"Authorization": f"Bearer {token}"})
                with urllib.request.urlopen(authed, timeout=2) as r:
                    self.assertEqual(r.status, 200)
                    self.assertIn(b"cache_hit_ratio", r.read())
                passed = True
            finally:
                out = stop(proc)
                if not passed:
                    # The shell relays the core's log; without it a failure
                    # here says nothing about why the core went away.
                    print("---- shell/core output ----\n" + out[-6000:])

            time.sleep(0.5)
            check = subprocess.run(["kill", "-0", str(core_pid)], capture_output=True)
            self.assertNotEqual(check.returncode, 0, f"Core PID {core_pid} survived Shell shutdown!")
            print(f"Full lifecycle test passed! (Shell={proc.pid}, Core={core_pid}, Port={port})")

    def test_unknown_port_fail_closed(self):
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
        s.listen(1)
        try:
            with tempfile.TemporaryDirectory() as td:
                proc = spawn(port, td, td)
                time.sleep(1.0)
                out = stop(proc)
                self.assertIn("Port conflict detected", out, "Failed to detect port conflict")
                self.assertNotIn("Assuming observer mode", out, "Must not assume observer mode on foreign port")
                print("Fail-closed foreign port test passed!")
        finally:
            s.close()

    def test_dual_instance_flock(self):
        port = get_free_port()
        with tempfile.TemporaryDirectory() as td:
            p1 = spawn(port, td, td)
            time.sleep(1.2)
            p2 = spawn(port, td, td)
            time.sleep(1.2)
            out1 = stop(p1)
            out2 = stop(p2)
            self.assertIn("flock lock acquired", out1, "P1 failed to acquire flock")
            self.assertIn("flock EWOULDBLOCK", out2, "P2 failed to detect flock contention")
            print("Dual instance flock test passed!")

    def test_core_crash(self):
        port = get_free_port()
        with tempfile.TemporaryDirectory() as td:
            proc = spawn(port, td, td)
            # 1.5 s used to be shorter than a cold shell+core start on a busy
            # CI runner (this test runs first in the suite); the other tests
            # wait 10 s for the same token file.
            core_pid, _ = read_token(td, timeout=10.0)
            self.assertIsNotNone(core_pid)
            os.kill(core_pid, signal.SIGKILL)
            # The termination handler dispatches its NSLog to the main run
            # loop; give a busy CI runner room to process it before we drain
            # the shell's pipes.
            time.sleep(2.0)
            out = stop(proc)
            self.assertTrue(
                "Crash alert suppressed by RELAYHUB_NO_ALERT_MODAL" in out or "Core 服务异常退出" in out,
                f"watchdog did not report the crash: {out[-800:]}")
            print("Core crash watchdog test passed!")


if __name__ == "__main__":
    unittest.main(verbosity=2)
