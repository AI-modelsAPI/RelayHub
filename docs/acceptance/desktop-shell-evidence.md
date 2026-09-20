# 桌面壳加固与生命周期实实验收证据 (desktop-shell-evidence.md)

## 1. 任务目标与加固设计

本次加固重点解决 macOS 桌面壳（Objective-C Cocoa + WebKit + Go Core 二进制托管）的生命周期完整性、实例所有权与看门狗告警：
1. **单实例所有权判定**：
   - 桌面壳在启动时检查指定端口与数据目录下的所有权令牌文件 `relayhub.token`（包含 `pid`, `token`, `shell_pid`, `timestamp`）。
   - 若已有活跃 Core 进程或端口被占用，新打开的桌面壳作为**观察者模式 (Observer Mode)** 接入，**不**持有 Core 启停所有权，也不会在退出时误关已运行实例。
   - 启动时自动通过 `proc_pidpath` 校验旧令牌记录的 PID，清除死进程遗留的陈旧 (stale) 令牌文件。
2. **进程身份校验与防误杀 (PID Reuse Protection)**：
   - 启动 Core 子进程后，桌面壳调用 macOS 底层内核接口 `proc_pidpath` 立即校验新进程的实际镜像路径，确保可执行文件名确为 `relayhub-core` 或 `relayhub`，防止 PID 竞争碰撞。
   - 在退出停止 Core (`stopCore`) 时，在下发 `SIGTERM` 及后续 `SIGKILL` 升级前，再次通过 `proc_pidpath` 校验 PID 身份，防止退出期间 PID 被系统复用而误杀无辜系统进程。
   - 桌面壳仅杀死由自己启动并持有所有权令牌的进程，观察者实例退出时直接断开连接，不向外部 Core 发送信号。
3. **Core 异常退出看门狗 (Watchdog Alert)**：
   - 通过 `NSTask.terminationHandler` 监听 Core 子进程退出事件。
   - 若桌面壳本身处于正常退出阶段（`isTerminating`），则安静回收；
   - 若 Core 发生非预期崩溃、被外力 SIGKILL 或崩溃退出，看门狗立即捕获并向主线程派发关键警告弹窗（`NSAlertStyleCritical`），并在状态栏显示 `⚡ RelayHub [Core 崩溃]` 及退出状态码，同时清理令牌文件。
   - 提供 `RELAYHUB_NO_ALERT_MODAL=1` 环境变量，方便非交互模式/探针自动化验收时不阻塞主 RunLoop。
4. **优雅退出信号捕获**：
   - 注册 C 语言底层 `SIGTERM` 与 `SIGINT` 信号处理器，将系统退出信号派发至 `[NSApp terminate:nil]`，确保无论通过 UI 菜单退出还是系统/终端给 Shell 发送信号，都能触发 `applicationWillTerminate` 完整清理生命周期。

---

## 2. RED 探针与演进过程

扩展 `docs/acceptance/packaging_probe.py` 进行真实验收：
- 真实调用 `scripts/build-macos.sh` 打包 amd64 和 arm64 的 `.app` 与 `.dmg` 产物。
- 启动原生打包产物 `dist/RelayHub-amd64.app/Contents/MacOS/RelayHub`，指定隔离的 `--data-dir`。
- 采用非死等轮询探测 `http://127.0.0.1:8790/healthz` 达到 200。
- 验证内嵌 UI `http://127.0.0.1:8790/` 响应 HTTP 200 并包含 `RelayHub` 页面标记。
- 向桌面壳发送正常终止信号退出，断言 Core 子进程被完整回收（无孤儿进程残留）。

### RED 阶段实测结果（加固前）：
```json
{
  "smoke_negative_control": {
    "exit": 1,
    "accepted_fatal_output": false
  },
  "desktop_shell_lifecycle": {
    "shell_launched": true,
    "healthz_200": true,
    "ui_200": true,
    "core_spawned_with_ownership": true,
    "core_cleaned_on_exit": false,
    "core_pid": 90206
  },
  "healthcheck_nondefault_local_http": 0,
  "healthcheck_closed_port": 1
}
```
**RED 结论**：加固前 Shell 退出时未捕获 SIGTERM 信号或未级联终止 Core，导致 `core_cleaned_on_exit: false`，Core 进程变为 PPID 1 的孤儿进程滞留后台。

---

## 3. GREEN 阶段真实执行证据

修改 `desktop/macos/RelayHubApp/main.m`，完善信号捕获、所有权判定、身份安全校验与看门狗，重新构建后执行验收。

### 3.1 `python3 docs/acceptance/packaging_probe.py` 输出
```
curl: (7) Failed to connect to 127.0.0.1 port 62079 after 1 ms: Couldn't connect to server
{
  "smoke_negative_control": {
    "exit": 1,
    "accepted_fatal_output": false
  },
  "desktop_shell_lifecycle": {
    "build_package_ok": true,
    "shell_launched": true,
    "healthz_200": true,
    "ui_200": true,
    "core_spawned_with_ownership": true,
    "core_cleaned_on_exit": true,
    "core_pid": 93137
  },
  "healthcheck_nondefault_local_http": 0,
  "healthcheck_closed_port": 1
}
```
所有断言均为 `true`，退出码 0。

### 3.2 真实单实例与所有权冲突测试
测试同时启动 App 1 与 App 2（指向相同端口 8790）：
- App 1 作为所有者生成 `relayhub.token`，启动 Core 并正常运行。
- App 2 启动检测到 8790 端口已在使用中，进入 Observer 模式，**不**创建自己的 token 亦**不**强占 Core。
- 终止 App 2，App 1 的 Core 依然持续运行，健康检查持续返回 200（未发生误杀）。
- 终止 App 1，持有所有权的 Shell 优雅关闭 Core，移除 token 文件，全局进程无任何 `relayhub-core` 残留。

### 3.3 真实 Core 崩溃看门狗测试
模拟通过 `kill -9` 强杀 Core 进程：
- 桌面壳 `task.terminationHandler` 捕获到退出状态码 9；
- 看门狗触发 `showCoreCrashAlert` 告警，状态栏变更为 `⚡ RelayHub [Core 崩溃]`；
- 状态栏文本更新为 `Core 服务异常退出 (退出码: 9)`；
- 自动清理失效所有权 token 文件。

### 3.4 `bash tests/macos/smoke.sh` 冒烟测试
```
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
```
退出码 0，全量验证通过。

---

## 4. 边界与未完成项
1. **修改范围限制**：本次严格限定在 `desktop/macos/`、`scripts/build-macos.sh`、`tests/macos/`、`docs/acceptance/`，未改动任何 `internal/`、`cmd/`、`docker/`、`web/` 文件。
2. **Git 操作遵循规范**：未进行 `git commit` 或 `git push`。
3. **未完成项如实说明**：
   - ARM64 架构二进制在当前 Intel x86_64 物理机上通过了 lipo 格式验证与 DMG 完整性验证，但因无 ARM 硬件环境未作原生指令集运行（与前序报告一致）。
   - `internal/browser` 中的 `TestCDPExecutor_RealChrome` 单元测试属于 CDP 自动签到模块，需要外部真实 Chrome 与网络环境，按任务边界规范未越界修改。
