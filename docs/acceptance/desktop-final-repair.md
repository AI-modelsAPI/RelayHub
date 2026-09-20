# macOS 桌面壳与验收探针修复与真实验收报告

## 1. 概述与修改范围
本次任务针对 macOS 桌面壳 (`desktop/macos/RelayHubApp/main.m`) 及探针 (`docs/acceptance/packaging_probe.py`) 存在的安全缺陷与并发互斥脆弱性进行了根本性修复，并补充了独立的 macOS 测试套件 (`tests/macos/test_desktop_lifecycle.py` 与 `tests/macos/test_full_suite.sh`)。遵循 RED → GREEN 驱动开发流程，所有用例均基于真实进程与真实网络端口验证通过。

### 修改与新增文件清单：
1. `desktop/macos/RelayHubApp/main.m`：重构 macOS 桌面壳生命周期控制、安全防护、信号处理、进程归属与互斥锁逻辑。
2. `docs/acceptance/packaging_probe.py`：重构探针断言体系，消除静态写死端口，增加严格 fail-closed/flock/崩溃及精确自身子进程归属校验。
3. `tests/macos/test_desktop_lifecycle.py`：新增独立 macOS 桌面壳端到端生命周期与故障注入测试套件。
4. `tests/macos/test_full_suite.sh`：新增包含构建冒烟、探针验收、桌面壳生命周期测试的一键运行验证脚本。
5. `docs/acceptance/desktop-final-repair.md`：本真实验收报告文档。

---

## 2. 缺陷根因分析与修复实现

### (1) 文件锁排他（flock 原子锁）取代 token 弱检测
- **缺陷**：原有逻辑仅依赖检查 `relayhub.token` 文件是否存在，多实例启动时存在严重竞争条件，可能导致两个主实例同时操作同一数据目录并启动冲突的 Core 进程。
- **修复**：在数据目录下引入 `relayhub.lock` 文件，在 Shell 启动时调用 `flock(fd, LOCK_EX | LOCK_NB)` 获取排他文件锁。持有该锁的 Shell 作为 Primary 实例独占管理 Core；未持有锁的实例自动判定为 Secondary/Observer，杜绝多主实例竞争与脑裂。

### (2) 端口占用 Fail-Closed 安全拦截（不盲连、不加载未知网页）
- **缺陷**：原有壳在端口被占用时，未验证对端身份即自动进入观察者模式，并在 `setupWindow` 中直接盲目加载 `http://127.0.0.1:<port>`，可能将未知或恶意本地服务的页面呈现给用户。
- **修复**：
  - `setupWindow` 中移除无条件盲目加载 `reloadWebView`。
  - 若端口已被占用，Shell 必须首先通过同步探针 `/api/v1/health` 验证对端是否返回合法的 RelayHub 标识（`{"status":"ok",...}`）。
  - 若对端为未知第三方服务，立即进入 **Fail-Closed** 状态：显示严重端口冲突告警，绝不启动本地 Core，且坚决不加载 WebView 网页，禁止作为观察者接入。

### (3) Core 进程归属强化与 PID 复用防御
- **缺陷**：原先仅通过 `proc_pidpath` 检查进程末尾名称，无法防御 PID 回绕复用攻击；同时 `stopCore` 存在按 PID 误杀其他同名进程的隐患。
- **修复**：
  - Shell 启动 Core 时记录由 `NSTask` 返回的句柄，并通过 `proc_pidinfo(PROC_PIDTBSDINFO)` 捕获并记录子进程的真实启动时间戳（`start_tvsec`）。
  - 在写入 `relayhub.token` 时，记录 `pid`、`token`、`shell_pid` 及 `start_time`。
  - 在任何杀进程与退出回收操作前，同时比对进程可执行路径、文件名与进程创建启动时间，并优先通过 `[NSTask terminate]` 回收属于自身的子进程；只有确认该 PID 仍为自身此前拉起的同一进程时才允许发送信号。

### (4) 信号安全修复：dispatch signal source 替换异步信号不安全的 C handler
- **缺陷**：原有 `main.m` 在 C-level `signal(SIGTERM, handleTerminationSignal)` 中直接调用 `NSLog` 以及 `dispatch_async`，违背 POSIX 异步信号安全原则。
- **修复**：将 SIGTERM 和 SIGINT 设为 `SIG_IGN`，并创建 GCD 调度源 `dispatch_source_create(DISPATCH_SOURCE_TYPE_SIGNAL, ...)` 在主运行循环（main runloop）中安全派发和调度 `[NSApp terminate:nil]`，彻底避免死锁与内存未定义行为。

### (5) 端口传参与 Core 管理地址对其
- **缺陷**：原先启动 Core 仅传入 `-full-stack` 与 `-data-dir`，Core 默认监听固定 8790 端口，当 Shell 指定 `--port` 时未透传给 Core。
- **修复**：Shell 启动 Core 时，严格传递 `-management-addr 127.0.0.1:<port>` 参数，保证 Core 管理接口端口与 Shell 窗口及观察者监听的端口完全同步。

### (6) 探针断言与自进程隔离
- **缺陷**：原 `packaging_probe.py` 仅输出布尔字典，未对失败执行断言退出；全局取第一个匹配的 Core PID 会误判现有外部进程；端口使用硬编码 8790 易导致环境碰撞。
- **修复**：
  - 动态分配空闲端口（`get_free_port()`）和隔离临时数据目录（`tempfile.TemporaryDirectory`）。
  - 严格通过 token 文件读取当前 Shell 写入的 `shell_pid` 与 `pid`，并断言其父进程（PPID）严格等于当前 Shell PID。
  - 加入对负面控制用例、端口冲突 Fail-Closed、Flock 互斥、Core 崩溃看门狗告警、Docker 探测的全流程严格 `assert` 判定，任何步骤异常立即退出非 0。

---

## 3. RED → GREEN 验证记录

### (1) RED 阶段证据
在修复前运行 `tests/macos/test_shell_red.py`：
- **测试 1（未知端口占用 Fail-Closed）**：外挂未知服务监听端口，原 Shell 输出 `Existing Core instance detected (Port 65239 in use or token present). Assuming observer mode.`，盲目进入观察者模式并试图加载网页。**断言失败（RED）**。
- **测试 2（双实例 Flock 互斥）**：原 Shell 启动未创建并持有 `relayhub.lock` 文件锁，两个实例无锁争用。**断言失败（RED）**。

### (2) GREEN 阶段测试输出
修复完成后执行 `tests/macos/test_full_suite.sh`（涵盖构建、smoke、探针、生命周期全套验证）：

```
========================================================
1. Smoke test: Build and package check (x86_64 + arm64)
========================================================
==> Running macOS build smoke test...
==> Building macOS bundle for arm64 (arm64)...
  Building Go Core for arm64...
  Compiling native macOS shell for arm64...
==> Packaging DMG for arm64...
created: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-apple-silicon.dmg
Created: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-apple-silicon.dmg
==> Building macOS bundle for amd64 (x86_64)...
  Building Go Core for amd64...
  Compiling native macOS shell for x86_64...
==> Packaging DMG for amd64...
created: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-intel.dmg
Created: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-intel.dmg
==> macOS build complete!
==> Validating created .app bundles and DMGs (Host architecture: x86_64)...
  [arm64] CFBundleExecutable 'RelayHub' found and executable: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-arm64.app/Contents/MacOS/RelayHub
  [arm64] Verified architecture via lipo: arm64
  [arm64] Verified DMG artifact: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-apple-silicon.dmg
  [arm64] Skipping execution test (host 'x86_64' != target 'arm64')
  [amd64] CFBundleExecutable 'RelayHub' found and executable: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-amd64.app/Contents/MacOS/RelayHub
  [amd64] Verified architecture via lipo: x86_64
  [amd64] Verified DMG artifact: /Users/zhangguojun/projects/Autoproxy/dist/RelayHub-macOS-intel.dmg
  [amd64] Host matches target architecture, testing executable launch...
  [amd64] Successfully launched executable: relayhub 0.0.0-dev (commit unknown, built unknown, go1.27.1 darwin/amd64)
==> macOS packaging smoke test passed successfully!
========================================================
2. Acceptance Probe (packaging_probe.py)
========================================================
curl: (7) Failed to connect to 127.0.0.1 port 49644 after 0 ms: Couldn't connect to server
{
  "smoke_negative_control": {
    "exit": 1,
    "rejected_fatal_fixture": true
  },
  "desktop_shell_lifecycle": {
    "build_package_ok": true,
    "shell_launched": true,
    "healthz_200": true,
    "ui_200": true,
    "core_spawned_with_ownership": true,
    "core_cleaned_on_exit": true,
    "core_pid": 1733
  },
  "unknown_port_fail_closed": true,
  "dual_instance_flock": true,
  "crash_watchdog_handled": true,
  "healthcheck_nondefault_local_http": 0,
  "healthcheck_closed_port": 1
}
========================================================
3. Desktop Lifecycle Suite (tests/macos/test_desktop_lifecycle.py)
========================================================
Full lifecycle test passed! (Shell=1775, Core=1776, Port=49647)
Fail-closed foreign port test passed!
Dual instance flock test passed!
Core crash watchdog test passed!

ALL MACOS DESKTOP TESTS PASSED!
========================================================
ALL TEST SUITES PASSED SUCCESSFULLY!
========================================================
```

---

## 4. 实测与推断区分

- **实测验证项 (Verified on Host)**：
  1. 当前宿主机架构为 `x86_64` (macOS 14.8.5)，`x86_64` 桌面壳生命周期、动态端口分配、flock 锁竞争、未知服务端口 Fail-Closed 拦截、Core 异常退出看门狗告警均通过真实子进程与套接字实测。
  2. 双架构 DMG 构建脚本 `scripts/build-macos.sh` 正确产出 `RelayHub-arm64.app`、`RelayHub-amd64.app`、`RelayHub-macOS-apple-silicon.dmg`、`RelayHub-macOS-intel.dmg`，并通过 `lipo` 检验各自真实架构。
  3. `docs/acceptance/packaging_probe.py` 与 `tests/macos/smoke.sh` 均能在故障夹具注入时成功失败退出，负面对照验证有效。
  4. 进程清理验证：所有测试均使用专属隔离环境及自持有进程，测试完成后系统无残留 Core 或 Shell 孤儿进程。

- **推断/受限事项 (Inferred / Environmental Limitations)**：
  1. **无 ARM64 实机环境**：当前测试机架构为 `x86_64`，因此对于 `arm64` 产物仅通过 `lipo` 静态校验了 Mach-O 目标二进制结构及 DMG 格式打包，未能在 Apple Silicon 真实芯片硬件上直接加载运行；该项推断只要 Clang/Go 跨架构交叉编译正确，且未引入体系结构依赖汇编，则行为与 x86_64 对等。
  2. **原生 GUI 窗口截图**：在远程无头环境/受限会话中，macOS 辅助功能与窗口服务器未授予对纯后台脚本窗口对象的 AppleScript/Screencapture 访问权限（返回 `-1728` 窗口不存在），未能保存物理屏幕截图；但已通过 `RELAYHUB_NO_ALERT_MODAL=1` 及日志/端口/WebKit请求行为完整验收了 UI 200 与看门狗告警路径。
  3. **未修改任何 Go 核心代码，未执行 git commit/push**，严格遵守主助手协同边界。
