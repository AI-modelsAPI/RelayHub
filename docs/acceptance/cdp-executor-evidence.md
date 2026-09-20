# Tier 1 CDP 自动签到执行器验收证据

**生成时间**：2026-09-18  
**目标组件**：`internal/browser/cdp`、`internal/browser/executor.go`、`internal/checkin/scheduler.go`、`tests/checkin_cdp_test.go`  
**测试平台**：macOS (darwin/amd64), Go 1.27.1, 宿主真实 Google Chrome 153.0.8010.47

---

## 1. 真实浏览器环境与探针验证

- 宿主系统 Chrome 探测：
  ```bash
  /Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome --version
  ```
  输出：
  ```
  Google Chrome 153.0.8010.47
  ```
- `browser.Detect()` 探测结果：
  - `Available`: `true`
  - `Path`: `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`
  - `Version`: `Google Chrome 153.0.8010.47`
  - `Tier`: `1`

---

## 2. CDP 执行器实现与架构约束

1. **零外部新依赖**：
   - 采用纯 Go 编写的标准 RFC 6455 握手与轻量 WebSocket 帧协议（`internal/browser/cdp/client.go`），直接与 Chrome Remote Debugging Port 建立连接与 JSON-RPC 消息收发。
   - `go.mod` / `go.sum` 保持纯净，未引入 Node/Playwright/Puppeteer 等额外外部运行时。

2. **多账号隔离 User Data Dir**：
   - 复用 `browser.ProfileDir(dataDir, providerID, channelID)`，遵循 B1/B2 隔离防碰撞规范，每一账号均分配独立 `--user-data-dir`。

3. **生命周期与进程树回收**：
   - 由 `browser.Runtime` 统一以单独进程组（`Setpgid = true`）启动浏览器进程。
   - 执行完成后 `Runtime.Close()` 执行 SIGTERM/SIGKILL 降级回收，并排空孤儿与助手后代进程，防止僵尸与残留。

4. **Turnstile 降级防护与 Fail-Closed**：
   - 当页面处于 Cloudflare Turnstile 人机交互挑战态（如 `[data-turnstile-status="challenge|interactive|failed"]`）或超时时，执行器明确返回 `ErrCDPNeedManual`。
   - `internal/checkin/Scheduler` 捕获该错误后，将其状态记为 `StatusNeedManual`，落库 `CheckinRecord{Status: "need_manual"}`，禁止假报成功（No fake success）。

---

## 3. 测试与执行证据

### 3.1 RED 测试阶段与复现

首次编写针对真实 Chrome CDP 的用例时（先写 RED 测试）：
```
=== RUN   TestCDPExecutor_RealChrome
=== RUN   TestCDPExecutor_RealChrome/success_flow
    executor_test.go:98: expected success, got error: checkin flow timed out: context deadline exceeded
=== RUN   TestCDPExecutor_RealChrome/turnstile_downgrades_to_need_manual
    executor_test.go:124: expected ErrCDPNeedManual, got: checkin flow timed out: context deadline exceeded (res: {Success:false Reward: Message:})
=== RUN   TestCDPExecutor_RealChrome/timeout_returns_error
--- FAIL: TestCDPExecutor_RealChrome (27.78s)
    --- FAIL: TestCDPExecutor_RealChrome/success_flow (15.22s)
    --- FAIL: TestCDPExecutor_RealChrome/turnstile_downgrades_to_need_manual (10.27s)
    --- PASS: TestCDPExecutor_RealChrome/timeout_returns_error (2.10s)
FAIL
```
原因分析：Runtime.evaluate 返回结果结构中为 `{ "result": { "value": ... } }`，解析字段路径匹配修正后转为 GREEN。

### 3.2 GREEN 测试阶段完整输出

执行 `go test -v -race ./internal/browser/... ./internal/checkin/...`:
```
=== RUN   TestProfileDir_CollisionResistance
--- PASS: TestProfileDir_CollisionResistance (0.00s)
=== RUN   TestRuntime_ProcessTreeReaping
--- PASS: TestRuntime_ProcessTreeReaping (0.08s)
=== RUN   TestDetect_RealSystemChrome
--- PASS: TestDetect_RealSystemChrome (0.10s)
=== RUN   TestDetect_EnvVarOverride
--- PASS: TestDetect_EnvVarOverride (0.01s)
=== RUN   TestCDPExecutor_RealChrome
=== RUN   TestCDPExecutor_RealChrome/success_flow
=== RUN   TestCDPExecutor_RealChrome/turnstile_downgrades_to_need_manual
=== RUN   TestCDPExecutor_RealChrome/timeout_returns_error
--- PASS: TestCDPExecutor_RealChrome (6.41s)
    --- PASS: TestCDPExecutor_RealChrome/success_flow (2.47s)
    --- PASS: TestCDPExecutor_RealChrome/turnstile_downgrades_to_need_manual (1.74s)
    --- PASS: TestCDPExecutor_RealChrome/timeout_returns_error (2.10s)
=== RUN   TestProfileDir
--- PASS: TestProfileDir (0.00s)
=== RUN   TestRuntime_ConcurrentClose
--- PASS: TestRuntime_ConcurrentClose (0.02s)
=== RUN   TestRuntime_NonTerminatingProcess
--- PASS: TestRuntime_NonTerminatingProcess (0.07s)
=== RUN   TestRuntime_ContextCancellation
--- PASS: TestRuntime_ContextCancellation (0.02s)
=== RUN   TestRuntimeLeaderExitRetainsDescendant
--- PASS: TestRuntimeLeaderExitRetainsDescendant (0.03s)
=== RUN   TestRuntime_LifecycleReap
--- PASS: TestRuntime_LifecycleReap (0.09s)
PASS
ok  	relayhub/internal/browser	7.892s

=== RUN   TestReviewDisabledProviderDoesNotExecute
--- PASS: TestReviewDisabledProviderDoesNotExecute (0.00s)
=== RUN   TestReviewPersistenceFailurePropagates
=== RUN   TestReviewPersistenceFailurePropagates/auto
=== RUN   TestReviewPersistenceFailurePropagates/manual
--- PASS: TestReviewPersistenceFailurePropagates (0.00s)
    --- PASS: TestReviewPersistenceFailurePropagates/auto (0.00s)
    --- PASS: TestReviewPersistenceFailurePropagates/manual (0.00s)
=== RUN   TestBuiltinAdaptersFixtureDriven
=== RUN   TestBuiltinAdaptersFixtureDriven/agentrouter
=== RUN   TestBuiltinAdaptersFixtureDriven/justdowork
=== RUN   TestBuiltinAdaptersFixtureDriven/gorouter
=== RUN   TestBuiltinAdaptersFixtureDriven/seekai
=== RUN   TestBuiltinAdaptersFixtureDriven/kktoken
--- PASS: TestBuiltinAdaptersFixtureDriven (0.00s)
=== RUN   TestSchedulerRunNowAndState
--- PASS: TestSchedulerRunNowAndState (0.55s)
=== RUN   TestCheckIn_NeedWebview_ManualFallback
--- PASS: TestCheckIn_NeedWebview_ManualFallback (0.68s)
=== RUN   TestScheduler_CDPExecutorIntegration
=== RUN   TestScheduler_CDPExecutorIntegration/cdp_success_marks_job_success
=== RUN   TestScheduler_CDPExecutorIntegration/cdp_turnstile_downgrades_to_need_manual
--- PASS: TestScheduler_CDPExecutorIntegration (0.79s)
    --- PASS: TestScheduler_CDPExecutorIntegration/cdp_success_marks_job_success (0.01s)
    --- PASS: TestScheduler_CDPExecutorIntegration/cdp_turnstile_downgrades_to_need_manual (0.01s)
PASS
ok  	relayhub/internal/checkin	3.086s
```

### 3.3 端到端真实夹具测试（E2E）

执行 `go test -v -race -run TestCDPExecutor_EndToEndFlow ./tests`:
```
=== RUN   TestCDPExecutor_EndToEndFlow
--- PASS: TestCDPExecutor_EndToEndFlow (9.11s)
PASS
ok  	relayhub/tests	10.225s
```
- 验证要点：
  1. 真实启动系统 Chrome（PID 分配、独立 Profile 路径分配）。
  2. 打开本地 HTTP 夹具页面，完成 `Page.enable`、`Runtime.enable`、`Page.navigate`。
  3. 执行按钮点击 `btn.click()`，轮询捕获成功结果 `$2.50`。
  4. SQLite 存储内生成 `CheckinRecord{Status: "success", Reward: "$2.50"}`。
  5. 遇到 Turnstile challenge 模拟门页面，降级返回 `ErrNeedManual`，生成 `CheckinRecord{Status: "need_manual"}`。
  6. 检查 `rt.ActiveProcesses() == 0` 以及系统的进程表，确认测试后所有浏览器子进程、辅助进程全部被彻底销毁。

---

## 4. 边界与如实报告（未完成项声明）

1. **夹具与真实站点边界声明**：
   - 本次完成的是 **Tier 1 CDP 浏览器执行器基础设施与真实进程控制、页面交互流、Turnstile 门控降级、调度器串联与数据库持久化**。
   - 本次测试验证基于**本地真实 HTTP 夹具服务（httptest.Server）**，**严禁且并未访问或冒充**任何真实第三方站点（AgentRouter / JustDoWork / GoRouter / SeekAI / KKtoken AI）的服务端生产环境。
   - 真实站点的 Cloudflare Turnstile token 回填与站方业务验证在后续需要进一步对接站点真实流程。

2. **未覆盖范围**：
   - Tier 2 `chrome-headless-shell` 的全自动后台下载与哈希校验流程（按计划在机器未安装任何 Chromium 浏览器时触发）。
   - 用户界面的桌面 WebView 可视化人工完成交互弹窗（当前已支持降级记录并给出 manual 链接）。
