import os, sys, time, socket, subprocess, tempfile, pathlib, urllib.request, signal

ROOT = pathlib.Path(__file__).resolve().parents[2]
BINARY = ROOT / "dist/RelayHub-amd64.app/Contents/MacOS/RelayHub"

def get_free_port():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(('127.0.0.1', 0))
    port = s.getsockname()[1]
    s.close()
    return port

def test_full_lifecycle():
    port = get_free_port()
    with tempfile.TemporaryDirectory() as td:
        env = dict(os.environ, HOME=td, RELAYHUB_NO_ALERT_MODAL='1')
        data_dir = pathlib.Path(td) / "data"
        data_dir.mkdir()
        
        proc = subprocess.Popen([str(BINARY), '--port', str(port), '--data-dir', str(data_dir)],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        core_pid = None
        core_token = None
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                tfile = data_dir / "relayhub.token"
                if tfile.exists():
                    for line in tfile.read_text().splitlines():
                        if line.startswith("pid="):
                            core_pid = int(line.split("=")[1])
                        elif line.startswith("token="):
                            core_token = line.split("=")[1]
                if core_pid:
                    break
                time.sleep(0.05)
            
            assert core_pid is not None, "Core PID not recorded in token"
            assert core_token is not None, "Core UUID token not recorded in token"
            
            # Check ppid of core
            ps_ppid = subprocess.run(['ps', '-p', str(core_pid), '-o', 'ppid='], capture_output=True, text=True).stdout.strip()
            assert int(ps_ppid) == proc.pid, f"Core PPID {ps_ppid} does not match Shell PID {proc.pid}"

            # Check healthz & UI on dynamic port
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=2) as r:
                assert r.status == 200, f"healthz returned {r.status}"
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=2) as r:
                assert r.status == 200, f"UI returned {r.status}"

        finally:
            proc.terminate()
            proc.wait(timeout=5)

        time.sleep(0.5)
        # Check Core exited
        check = subprocess.run(['kill', '-0', str(core_pid)], capture_output=True)
        assert check.returncode != 0, f"Core PID {core_pid} survived Shell shutdown!"
        print(f"Full lifecycle test passed! (Shell={proc.pid}, Core={core_pid}, Port={port})")

def test_unknown_port_fail_closed():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(('127.0.0.1', 0))
    port = s.getsockname()[1]
    s.listen(1)

    with tempfile.TemporaryDirectory() as td:
        env = dict(os.environ, HOME=td, RELAYHUB_NO_ALERT_MODAL='1')
        proc = subprocess.Popen([str(BINARY), '--port', str(port), '--data-dir', td],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        time.sleep(1.0)
        proc.terminate()
        stdout, stderr = proc.communicate(timeout=5)
        out = stdout + stderr
        s.close()
        assert "Port conflict detected" in out, "Failed to detect port conflict"
        assert "Assuming observer mode" not in out, "Must not assume observer mode on foreign port"
        print("Fail-closed foreign port test passed!")

def test_dual_instance_flock():
    port = get_free_port()
    with tempfile.TemporaryDirectory() as td:
        env = dict(os.environ, HOME=td, RELAYHUB_NO_ALERT_MODAL='1')
        p1 = subprocess.Popen([str(BINARY), '--port', str(port), '--data-dir', td],
                              env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        time.sleep(1.2)
        p2 = subprocess.Popen([str(BINARY), '--port', str(port), '--data-dir', td],
                              env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        time.sleep(1.2)
        p1.terminate()
        p2.terminate()
        out1 = "".join(p1.communicate(timeout=5))
        out2 = "".join(p2.communicate(timeout=5))
        assert "flock lock acquired" in out1, "P1 failed to acquire flock"
        assert "flock EWOULDBLOCK" in out2, "P2 failed to detect flock contention"
        print("Dual instance flock test passed!")

def test_core_crash():
    port = get_free_port()
    with tempfile.TemporaryDirectory() as td:
        env = dict(os.environ, HOME=td, RELAYHUB_NO_ALERT_MODAL='1')
        proc = subprocess.Popen([str(BINARY), '--port', str(port), '--data-dir', td],
                                env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        core_pid = None
        for _ in range(30):
            tf = pathlib.Path(td) / "relayhub.token"
            if tf.exists():
                for line in tf.read_text().splitlines():
                    if line.startswith("pid="):
                        core_pid = int(line.split("=")[1])
                        break
            if core_pid:
                break
            time.sleep(0.05)
        assert core_pid is not None
        os.kill(core_pid, 9)
        time.sleep(0.8)
        proc.terminate()
        out = "".join(proc.communicate(timeout=5))
        assert "Crash alert suppressed by RELAYHUB_NO_ALERT_MODAL" in out or "Core 服务异常退出" in out
        print("Core crash watchdog test passed!")

if __name__ == '__main__':
    test_full_lifecycle()
    test_unknown_port_fail_closed()
    test_dual_instance_flock()
    test_core_crash()
    print("\nALL MACOS DESKTOP TESTS PASSED!")
