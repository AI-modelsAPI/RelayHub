# RelayHub 全量代码审计报告

- **审计对象**：`/Users/zhangguojun/projects/Autoproxy`（RelayHub，Go 1.27，12,367 行 Go + 1,556 行 JS）
- **基准文档**：`docs/superpowers/specs/2026-09-11-relayhub-design.md`（553 行设计规格）、`docs/superpowers/plans/2026-09-12-relayhub-implementation.md`（15 任务实施计划）、`.superpowers/axonhub-replication-plan.md`（AxonHub 复刻计划）
- **审计方式**：静态阅读 + **可执行探针复现**（每条 P0/P1 结论都在真实进程/真实 SQLite/真实 HTTP 上跑出来，见"复现证据"）
- **审计结论**：**项目当前不可用于其宣称的核心场景。** 编译、`go vet`、`gofmt`、现有 `go test ./...` 全绿，但绿灯是假象——**核心链路根本没有被测试覆盖**。进度账本 `progress.md` 声称"15/15 任务全部完成、Deferred register: None"，与实际严重不符。

---

## 0. 给修复者（Gemini 3.8 Flash）的执行须知

1. **必须先复现再修**。每条 Finding 都附了"复现证据"（真实命令 + 真实输出）。改之前先写一个 RED 测试把该缺陷复现出来，再修到 GREEN。不允许"看起来合理"的推测性修复。
2. **不允许删测试/改测试让它过**。不允许把断言放宽。
3. **不允许伪造输出**。跑不通就如实报告。
4. 修完每一条，跑：`gofmt -l cmd internal tests && go vet ./... && go test -race ./... -count=1`。
5. 优先级严格按 P0 → P1 → P2 → P3。**P0 不修完不要动 P2/P3**（尤其不要先去清理死代码）。
6. Go 环境：`export PATH=$PATH:$HOME/homebrew/bin`（go1.27.1 darwin/amd64）。

---

## 0.5　修复进度（主审计方独立复验，非子代理自报）

**第一批已验证修复**（复验方式：跑父方自己的探针，非采信子代理报告）

| 缺陷 | 状态 | 复验证据 |
|---|---|---|
| P0-1 协议命名 | ✅ 已修 | 用 `web/app.js` 原样 payload（`openai-chat`）建资源 → Gateway 200 |
| P0-2 快照不刷新 | ✅ 已修 | **不重启进程、不手工 Catalog.Load** → 直接 200；e2e 里的手工回填后门已删除 |
| P0-3 凭据未注入 | ✅ 已修 | 上游实收 `Authorization: Bearer sk-rea…cret`（日志自动脱敏） |
| P0-5 nil logger panic | ✅ 已修 | 错误路径不再崩；wiring 改传 `logging.New(os.Stdout)` |
| P1-8 无法签发 key | ✅ 已修 | `POST /api/v1/keys → 201`；`DELETE` 后 Gateway **立即 401**（符合规格 §8.4） |
| P0-6 调度器三缺陷 | ✅ 已修 | 重启不再 panic；记录带 UUID 可连续落库；ctx 取消后 0.18s 返回；另补 0-10 分钟随机首跑延迟 |
| P0-7 导出秘密区 | ✅ 已修 | 真实凭据加解密往返通过；伪造 checksum 被拒；覆盖失败触发回滚 |

全量门禁：`go build` OK、`gofmt -l cmd internal tests` 无输出、`go vet ./...` 无输出、`go test ./... -count=1` 20 包全绿。

**第二批已验证修复**（同样由主审计方独立探针复验）

| 缺陷 | 状态 | 复验证据 |
|---|---|---|
| P0-8 外键静默失效 | ✅ 已修 | `PRAGMA foreign_keys = 1`；插入孤儿外键实测被拒 `FOREIGN KEY constraint failed (787)` |
| P1-1 用量记录未接线 | ✅ 已修 | 一次网关请求后 `/api/v1/usage` 有 1 条记录，`input=7 output=11` 精确；载荷中无 prompt 内容 |
| P1-2 `/v1/models` 空列表 | ✅ 已修 | 返回 `{"data":[{"id":"m1","object":"model","owned_by":"relayhub"}]}` |
| P1-4 协议转换未实现 | ✅ 已修 | Anthropic 客户端打 OpenAI 上游 → 收到 `{"type":"message","content":[…],"stop_reason":"end_turn"}`（不再是 `choices[]`）；反向 OpenAI 客户端打 Anthropic 上游 → 收到 `{"choices":[…],"object":"chat.completion"}`，上游实收 `{"system":"be brief","max_tokens":4096,"content":[{"type":"text"}]}` |
| P1-5 模型组失效 | ✅ 已修 | 组名/组 ID 均可命中并转发 200 |
| P1-3 + P2-1 代理策略与 config | ✅ 已修 | 默认策略 `open`，公网目标经 HTTP 代理实测 200；`local_only` 显式可选；启动日志打印当前策略 |
| P1-9 适配器猜端点/谎报成功 | ✅ 已修 | 产出 `docs/adapter-contracts.md`（五站逐条对照 autosign 源码）；Turnstile 站纯 HTTP 一律 `NeedWebview` 拒绝假签；五站各有 fixture 单测（含 401/403/429/5xx/字段缺失）；`wiring.go` 已改为 `NewWithSecrets(nil, secStore)`，凭据引用不再当密钥发出 |

**主审计方在复验中修掉的并行冲突**（两个子代理同时改 `wiring.go` 造成）

- `wiring.go:184-185` 用 `%s` 格式化 `proxy.TargetPolicy` 结构体，`go vet` 失败 → `go test ./internal/app` 报 `[build failed]`，**仓库一度处于不可测状态**。已加 `describeTargetPolicy()` 修正。
- 子代理 3 只预留了 seam 未接线（它被明确禁止改 wiring），由主审计方接上五个 `NewWithSecrets`。

**第三批已验证修复**（主审计方独立探针复验，真实进程 + 真实 SQLite + 真实 HTTP）

| 缺陷 | 状态 | 复验证据 |
|---|---|---|
| P1-6 CLI 同步接线 | ✅ 已修 | `GET /api/v1/cli-sync` → `supported:true` + 三个真实检测路径；`preview` 签发真实 key（`rh_ZXw…`）并返回真实 diff；`apply` 写盘成功、二次 apply 产生真实备份 `config.yaml.20260914165243.bak`；同步记录落库 |
| P1-6 假密钥 fail-closed | ✅ 已修 | 删除 `local-relayhub-key` 兜底；无 key 时 apply 拒写且不留残file。实测：`.env` 里的 key 打网关 **400**（认证通过、仅缺路由），伪造 key 与旧假密钥均 **401** |
| P1-6 前端假成功 toast | ✅ 已修 | `web/app.js:775` 的 `syncCLI(){showToast(已保存)}` 已删除；改为 preview 弹真实 diff 对话框 → 用户确认 → apply；失败显示真实错误。`cli-sync` 页此前从未接入 `switchTab`（切过去什么都不加载），一并修复 |
| P1-7 导入导出接线 | ✅ 已修 | 导出含凭据实测明文不泄漏（密文 136B）；预览正确识别 2 处冲突；**篡改包 → 400 tampered_package**；**错密码 → 400 decryption failed** |
| P1-7 跨实例凭据往返 | ✅ 已修 | 在 master key 完全不同的干净实例导入后，凭据被解密→用新 key 重新加密入库（密文不同，非照搬），最终经网关**以明文注入上游、端到端 200** |

**第三批中新发现并修复的缺陷**（审计初版与前两批均遗漏）

### 新-1　签到测试是定时炸弹：fixture 把"今天"写死

`internal/adapter/fixtures/agentrouter/responses.json` 等把 `checkin_date` / `created_at` 硬编码为 `2026-09-14`，而 `TodayBonus` / `CheckinStatus` 按**北京时间当天**比对。测试只在编写当天为绿，之后每天必红。

实测（系统日期 2026-09-15）：

```
--- FAIL: TestAgentRouter_All/CheckIn_WithTodayBonusInLog
    adapter_test.go:114: CheckIn returned error: account requires relogin to trigger daily reward
```

**修法**（未放宽断言）：fixture 改用 `{{TODAY}}` / `{{YESTERDAY}}` 占位符，`testhelper.LoadFixture` 在加载时按北京时间替换为真实日期。任何一天都成立。

### 新-2　Claude Code 端点被写成 `/v1`，Anthropic API 不接受

接线初版让 `clisync.Service` 用**一个统一 BaseURL** 覆盖所有 syncer，结果把 `http://127.0.0.1:8789/v1` 写进了 `ANTHROPIC_BASE_URL` —— 而 `claude.go` 自己的默认值和 `Verify` 都明确是不带 `/v1` 的。`Verify` 侥幸通过（因为两侧用了同一个错 URL），但配置对真实 Anthropic 客户端是坏的。

**修法**：Service 只持有 `GatewayAddr`（host:port），端点后缀由各 syncer 按自身协议决定；补 `TestEndpointSuffixDiffersPerProtocol` 锁死该差异。实测：claude=`http://127.0.0.1:8789`（无 /v1），codex/hermes=`…/v1`。

### 新-3　Hermes `.env` 写入错误被静默吞掉

`hermes.go` 两处 `_ = s.Engine.WriteAtomic(envPath, …)`，写失败也会向上报成功。已改为返回错误。

**第三批顺带修复的工程问题**

- `Dockerfile` 拷 `web/`（不被嵌入的那份）而非 `internal/web/`，已修正并加注释说明单一事实来源。
- 新增 `make check-web` 门禁：`web/` 与 `internal/web/` 不一致即 fail（`make test` 依赖它），并提供 `make sync-web`。这两份此前已漂移过。

**测试改动说明（均非放宽断言）**

- `TestUnsupportedMutationNeverClaimsSuccess`：cli-sync / import-export 已实现，不再返回 501，故从该用例移出；新增 `TestImplementedMutationsRejectBadInputWithoutClaimingSuccess` 保住原用例的真正不变式 —— **未发生变更的请求绝不能报 success**（覆盖 6 种坏输入）。
- `TestHermesSyncCycle`：改为先断言"无 key 时 apply 必须拒绝且不留残file"，再补 key 走通完整流程 —— 覆盖面比原来更强。

**仍未完成**

- P2-5 / P2-8 / P2-9 / P2-10（Docker 健康检查硬编码、macOS 产物名与 `.app` 可执行文件名、壳生命周期）由另一路负责，本轮未复验。
- BROWSER-PLAN 所述的浏览器签到运行时尚未实现。

---

**复验中新发现（审计初版遗漏，严重度 P0）**

### P0-8　SQLite 外键**全库静默失效**，所有 `REFERENCES` 约束形同虚设

`internal/storage/db.go:35`：

```go
d, err := sql.Open("sqlite", path+"?_foreign_keys=on")
```

`_foreign_keys=on` 是 **mattn/go-sqlite3（CGO）** 的 DSN 参数，而本项目用的是 **modernc.org/sqlite（纯 Go）**，后者只认 `_pragma=foreign_keys(1)`。未知 DSN 参数被静默忽略，不报错。

实测对照：

```
DSN  ?_foreign_keys=on          => PRAGMA foreign_keys = 0   ← 当前代码，外键关闭
DSN  ?_pragma=foreign_keys(1)   => PRAGMA foreign_keys = 1   ← 正确写法
```

后果：`001_initial.sql` 里精心写的 `ON DELETE RESTRICT` / `ON DELETE CASCADE` / 所有 `REFERENCES` **一条都没生效**。实测可以创建引用不存在 provider 与 model 的 `provider_model` 并返回 200：

```
POST /api/v1/provider-models {"provider_id":"nope","model_id":"nope"} -> 200
```

这直接推翻计划 Task 2 Step 3「添加外键、唯一约束」的验收，也让 `repository.ErrProviderReferenced` 的删除保护在真实数据库上失去底层兜底（目前只剩 `catalog` 内存层的一道检查，而 API 走的是 repository 而非 catalog）。

**修复要求**：改用 `_pragma=foreign_keys(1)`；连接建立后**回读 `PRAGMA foreign_keys` 断言为 1**，否则拒绝启动（fail-closed，避免下次换驱动又静默退化）；补一条回归测试：插入孤儿外键必须失败、删除被引用的 provider 必须被 RESTRICT 挡住。注意现网库 `~/Library/Application Support/relayhub/relayhub.db` 实测暂无孤儿数据（provider_models 0 行），开启外键不会因存量脏数据导致启动失败。

---

## 1. 严重度总览

| 级别 | 数量 | 含义 |
|---|---:|---|
| **P0 阻断** | 7 | 核心功能完全不工作 / 安全漏洞 / 数据丢失 |
| **P1 严重** | 9 | 主要功能缺失或错误，用户可感知 |
| **P2 中等** | 11 | 健壮性、规范、可维护性问题 |
| **P3 轻微** | 8 | 死代码、冗余、文档不一致 |

**一句话结论**：RelayHub 现在是一个"能启动、能显示界面、但不能真正代理 AI 请求、不能签到、不能记账、不能导出、且管理面存在 CSRF 的壳"。

---

## 2. P0 阻断级缺陷（7 条）

### P0-1 　通过 Web UI / 管理 API 创建的渠道**永远无法被路由命中**（协议字符串不匹配）

**位置**
- `web/app.js:594`、`web/app.js:725` 写入 `protocol:"openai-chat"` / `"anthropic-messages"`
- `internal/api/server.go:575` 自动同步模型时也写 `"openai-chat"`
- `internal/gateway/gateway.go:303-316` `endpointProtocol()` 返回 `"openai"` / `"anthropic"`
- `internal/router/selector.go:177-180` 严格相等比较：`if pm.Protocol != "" && protocol != "" && pm.Protocol != protocol { 排除 }`

**问题**：存在**两套互不兼容的协议命名**。设计规格 §5.1 定义的是 `openai-chat`/`anthropic-messages`；Gateway 内部用的是 `openai`/`anthropic`。UI 按规格写库，Router 按 Gateway 命名比对，于是**所有从界面创建的渠道在路由时 100% 被 `protocol mismatch` 排除**。

**复现证据**（router 单元级）：
```
PROBE1 resolve(openai) err = no available candidates: pm1: protocol mismatch
```
**复现证据**（真实全栈，按 UI 完全一致的 payload 建渠道后调用 Gateway）：
```
PROBE5 provider-models create -> 200 {"protocol":"openai-chat", ...}
PROBE5 gateway after API-only config -> 400 {"error":{"code":"client_error","message":"model not found: m1"}}
```

**修复要求**：定义**唯一**协议枚举（建议保留规格的 `openai-chat`/`openai-responses`/`anthropic-messages`/`gemini`），在 `endpointProtocol()` 返回规格名；或在 Router 入口做一次规范化映射。同时补一条迁移，把已有库里的值统一。**必须加回归测试：用 `web/app.js` 实际发送的 payload 建资源 → 调 Gateway → 断言 200。**

---

### P0-2 　管理 API 改动**不会刷新路由快照**，必须重启进程才生效

**位置**：`internal/app/wiring.go:75-96`

```go
_ = catalogSvc.Load(ctx)                       // 只在启动时读一次
rProviders, rChannels, ... := catalogSvc.Snapshot()
res := &router.Resolver{ Models: rModels, ProviderModels: pmList, ... }  // 值拷贝，此后永不更新
```

`Resolver` 持有的是**启动瞬间的快照副本**。`api.Server` 直接写 `repository`，从不通知 `catalog`，也从不重建 `Resolver`。全仓库只有 `tests/e2e` 和 `wiring.go` 调过 `Catalog.Load()`：

```
./tests/e2e/local_stack_test.go:82   _ = rt.Catalog.Load(repoCtx)
./internal/app/wiring.go:75          _ = catalogSvc.Load(ctx)
```

**注意**：现有的 `tests/e2e/local_stack_test.go` 之所以"通过"，正是因为它**在测试内部手工执行了 `Catalog.Load()` + 手工把快照塞回 `rt.Router.*`**（第 82-97 行）。这等于测试自己绕过了 Bug——**这是一个掩盖缺陷的测试，必须重写**。

**复现证据**：同 P0-1 的 PROBE5（创建后立即调用 → 404/400；手工 `Catalog.Load()` + 回填后 → 200）。

**修复要求**：`api.Server` 的所有写操作后触发 catalog 重载并原子替换 Resolver 快照（用 `atomic.Pointer[snapshot]` 或加读写锁），或让 Resolver 直接向 catalog 取值。**同时删掉 e2e 测试里的手工回填**，让它真正走 API → Gateway 全链路。

---

### P0-3 　Gateway **从不向上游注入凭据**，加密存储的 API Key 完全没有被使用

**位置**：`internal/app/wiring.go:212-217`

```go
gwHandler := gateway.New(gateway.Config{
    Resolver: res,
    Upstream: gateway.HTTPUpstream{},   // Credential 字段为 nil
    Health:   healthReg,
    Auth:     localKeys,
})
```

`gateway.HTTPUpstream.Credential`（`gateway.go:61`）是取上游密钥的唯一钩子，生产装配处**没有传**。于是 `gateway.go:92` 的 `if u.Credential != nil` 恒为 false，`requestHeaders()`（`gateway.go:319-320`）还会主动 `Del("Authorization")` / `Del("x-api-key")`。结果：**每一个转发到上游的请求都不带任何认证**，所有真实中转站必然 401。

`secrets.Store` 里辛苦加密的凭据除了被 `api.fetchModels` / `channelTest` 读取外，**在推理主链路上一次都没被读过**。

**复现证据**（真实上游服务器记录收到的请求头）：
```
PROBE6 gateway after manual snapshot refresh -> 200 {...}
PROBE6 upstream saw Authorization="" x-api-key=""
```

**修复要求**：在 wiring 里注入 `Credential: func(ctx, d) (string, error) { return secStore.Get(ctx, d.Channel.CredentialRef) }`，处理 ref 为空的情况，并根据 `decision.ProviderModel.Protocol` 选择 `Authorization: Bearer` 还是 `x-api-key`。**回归测试断言上游确实收到了预期 header，且日志中不出现明文密钥。**

---

### P0-4 　管理 API 存在 **CSRF**：任何被访问的网页都能改写全部配置并写入密钥

**位置**：`internal/api/server.go:353-372` `authorize()`

```go
if s.LocalOnly && s.Token == "" { return true }   // 本地模式下所有方法（含 POST/PUT/DELETE）无条件放行
```

`wiring.go:182-189` 生产装配正是 `LocalOnly: true` 且**不设 Token**。管理面没有 CSRF token、不校验 `Origin`/`Referer`、不要求自定义头。`Content-Type: text/plain` 的简单请求**不触发 CORS 预检**，浏览器会直接发出去；响应读不到不影响——写操作已经生效。

攻击面：把渠道 `base_url` 改成攻击者服务器 → 用户后续所有 AI 请求（含 prompt 内容）被劫持；或写入攻击者的 `credential_ref`。

**复现证据**（真实监听端口，跨站 Origin）：
```
PROBE17  cross-origin POST /api/v1/providers -> 200 {"provider":{"id":"evil",...}}
PROBE17b cross-origin POST /api/v1/secrets   -> 201 {"secret":{"ref":"evil-cred","stored":true}}
```

**修复要求**（fail-closed）：对所有变更方法强制要求 ①`Origin`/`Sec-Fetch-Site` 校验或 ②本地会话 token（页面加载时注入）或 ③自定义头 `X-RelayHub-Local: 1`（简单请求无法伪造自定义头，会触发预检）。三选一不够，建议 ①+③。**回归测试必须包含跨站 Origin 被拒绝的 RED→GREEN。**

---

### P0-5 　管理 API 内部错误路径**必定 panic**（`logging.New(nil)`）

**位置**：`internal/app/wiring.go:188` 传入 `Logger: logging.New(nil)`；`internal/logging/logger.go:16,30` 直接 `json.NewEncoder(l.out).Encode(...)`，`out == nil` 时空指针解引用。

触发点：`api/server.go:298-303` `fail()` 在任何内部错误（数据库故障、约束冲突等）时调用 `s.Logger.Event(...)` → **进程崩溃**。

**复现证据**：
```
PROBE2 CONFIRMED BUG: logging.New(nil).Event panics: runtime error: invalid memory address or nil pointer dereference
PROBE3 CONFIRMED BUG: management 500 path panics: runtime error: invalid memory address or nil pointer dereference
```

**修复要求**：两处都要修 —— ①`logging.New` 对 `nil` 兜底为 `io.Discard`；②wiring 传真实的 stdout/文件 logger（Docker 要求 stdout 输出日志，见规格 §12.3）。

---

### P0-6 　签到调度器：**重启必 panic**，且只有第一条签到记录能落库，退避 sleep 无法取消

三个独立缺陷，都在 `internal/checkin/scheduler.go`：

**(a) Start→Stop→Start panic**（`scheduler.go:53-54,89,101`）
`stopCh`/`stopped` 只在 `NewScheduler` 里建一次，`Stop()` 关闭 `stopCh`，第二次 `Start()` 的 `runLoop` 又 `defer close(s.stopped)` → `close of closed channel`。菜单栏"重启服务"/API 重启即触发。
```
PROBE11: panic: close of closed channel
  relayhub/internal/checkin.(*Scheduler).runLoop  scheduler.go:110
```

**(b) `CheckinRecord` 没有 ID，主键冲突**（`scheduler.go:212-216`）
构造记录时不填 `ID`，而 `checkin_records.id` 是 `TEXT PRIMARY KEY`。第一条写入空串成功，**之后每一条都失败**，且 `_ = s.repo.CreateCheckinRecord(...)` 吞掉了错误 —— 用户永远看不到签到历史。
```
PROBE19 second CheckinRecord insert err = UNIQUE constraint failed: checkin_records.id (1555)
```

**(c) 退避 `time.Sleep` 无视 context**（`scheduler.go:177-180`）
`time.Sleep(backoff)` 不可中断。`MaxRetries=3`、`BaseBackoff=5s` 时最坏 `10+20+40=70s`；用户点停止后进程仍卡住，`Shutdown` 的 10s 超时直接失效。
```
PROBE12 CONFIRMED BUG: RunNow still blocked >4s after ctx cancel
```

**额外**：`checkAndTrigger`（`scheduler.go:135-139`）对每个到期渠道无节制 `go RunNow(...)`，且 ticker 每分钟触发一次；`states` 为空时 `!ok` 成立，**启动后第一分钟会对所有渠道同时发起签到**，违反规格 §7.5 的"0-10 分钟随机延迟"。

**修复要求**：(a) 每次 `Start` 重建 channel；(b) 生成 UUID（项目已依赖 `github.com/google/uuid`），并且**不要吞错误**，记录失败要上报；(c) 用 `select { case <-time.After(backoff): case <-ctx.Done(): return ctx.Err() }`；(d) 启动首轮加随机延迟。

---

### P0-7 　"加密完整导出"里**根本没有凭据**，只有一句硬编码占位符

**位置**：`internal/export/export.go:55-63`

```go
if opts.IncludeSecrets && opts.Password != "" {
    secretPayload, _ := json.Marshal(map[string]string{"note": "encrypted credentials placeholder"})
    enc, err := EncryptPayload(secretPayload, opts.Password)
    ...
}
```

规格 §11.3 要求完整导出包含 Argon2id + AES-256-GCM 保护的**真实秘密区**。现在用户设了密码、拿到 `.rhx`、在新机器导入 —— **所有 API Key / Cookie 全部丢失**，而 UI 与文档都宣称"完整导出"。这是静默数据丢失。

同时 `Manifest.Checksum` 计算后**从未在导入时校验**（`import.go` 全文无 checksum 比对），规格 §11.3 明确要求"校验格式、版本、**完整性**"。被篡改的包会被直接接受。

**复现证据**：
```
PROBE16 decrypted secret section = {"note":"encrypted credentials placeholder"}
PROBE15 PreviewPackage(bogus checksum) -> err=<nil>   ← 伪造校验和被接受
```

**修复要求**：真正导出 `secret_values`（解密后重新用导出密码封装）；导入时严格校验 checksum，不符即拒绝；补失败回滚（`Apply` 现在在事务里，但 `_ = tx.UpdateProvider(...)` **吞掉了 overwrite 的错误**，`import.go:107,120,133,146`，必须改为返回错误触发回滚）。

---

## 3. P1 严重缺陷（9 条）

### P1-1 　用量/日志页面**永远为空**：`usage.Recorder` 从未装配
`wiring.go` 的 `gateway.Config` 没传 `Recorder`（`grep Recorder internal/app/wiring.go` 为空），`gateway.go:288-294` 的 `h.record()` 因此是死代码。规格 §10 要求概览展示"请求数、成功率"，UI 的 Usage/Logs 两个页面读 `/api/v1/usage`、`/api/v1/logs`，恒返回空。
```
PROBE13 /api/v1/usage records=0 summary={total_requests:0 ...}   ← 刚成功转发过一次带 usage 的请求
```
用户现网实例也印证：`request_records` 表 0 行。
**修复**：`Recorder: usage.RepositoryRecorder{Repo: repo}`。

### P1-2 　`GET /v1/models` 永远返回空列表
`gateway.go:154-157` 硬编码 `{"object":"list","data":[]}`。规格 §8.2/§8.3 要求返回可用模型。Codex / Claude Code / OpenAI SDK 拉不到任何模型。`progress.md` 把它记为 "Ruling: 显式推迟"，但 `Deferred implementation register: None` 又写"无推迟项"——**账本自相矛盾**。
```
PROBE14 GET /v1/models -> 200 {"data":[],"object":"list"}
```

### P1-3 　透明代理默认策略**拒绝一切非本地目标**，与项目核心目标直接冲突
`proxy.go:30` `LocalOnlyPolicy()` 只允许 loopback/link-local，`http_proxy.go:56` 和 `socks5_proxy.go:54` 在零值时套用它，而 `wiring.go:138-152` 构造代理时**不传 `TargetPolicy`** → 生产默认只能代理到 127.0.0.1。
用户的原始需求是"**所有站点（包括非这五个站点）的请求都由此软件代发**"。现在开箱即用的行为是全部拒绝。
```
PROBE7 public target through HTTP proxy -> error EOF   （403 后连接关闭）
```
`internal/config` 里已经定义了 `HTTPProxyTargetPolicy`/`SOCKS5TargetPolicy` 字段 —— 但**整个 `internal/config` 包在生产代码中零引用**（见 P2-1）。
**修复**：接通 config → wiring → proxy 的策略传递，默认给出可用（但有提示）的策略，并在 UI/文档里说明风险。

### P1-4 　**协议转换完全没有实现**：Anthropic 客户端打到 OpenAI 上游会拿到 OpenAI 响应体
`gateway/transform.go:37-49` 的 `transformRequest` **只替换 `model` 字段**，`transformResponse` 只替换 `model` 字段。`openai.go` / `anthropic.go` 两个文件各 13 行，只是 `Handler` 的空壳包装，没有任何协议差异处理。
```
PROBE18 /v1/messages -> 200 {"choices":[{"message":{"content":"ok"}}],"id":"x","model":"m1"}
PROBE18 upstream received path="/v1/messages" body={"max_tokens":16,"messages":[...],"system":"be brief","model":"up"}
```
Anthropic 客户端收到的是 `choices[]`（OpenAI 格式），会直接解析失败。规格 §8.2/§8.3 和计划 Task 7 明确要求"OpenAI/Anthropic 请求和响应转换"。SSE 同理：`writeSSE` 只做原样透传 + JSON 合法性检查。

### P1-5 　模型组（ModelGroup）在生产环境**完全失效**
`wiring.go:88-96` 构造 `router.Resolver` 时**没有设置 `Members` 字段**（`grep Members internal/app/wiring.go` 为空）。`selector.go:126` 遍历 `r.Members[groupID]` 恒为空 → 任何模型组请求返回 `no available members`。规格 §5.5 把模型组列为"用户分组和路由的主要入口"。
顺带：`Snapshot()` 返回 7 个值，wiring 用 `_` 丢掉了第 6 个（正是 members）。

### P1-6 　CLI 同步（Codex/Claude/Hermes）**没有接入任何 API 或 UI**
`internal/clisync` 包**生产代码零引用**（`prod_imports=0`）。`api/server.go:1338-1356` 的 `/api/v1/cli-sync` 非 GET 一律 501；UI `web/index.html:189-191` 三个"同步"按钮绑定的 `syncCLI()`（`app.js:775`）**只弹一个 toast，什么都不做**：
```js
function syncCLI(n){showToast(`${n}: ${T("toast_saved")}`);}   // 假成功
```
这是**对用户撒谎的 UI**（提示"已保存"但未做任何事），比不实现更糟。
```
PROBE10 POST /api/v1/cli-sync -> 501 {"message":"CLI synchronization service is not configured"}
```
另外 `clisync/hermes/hermes.go:152` 写死 `HERMES_CUSTOM_RELAYHUB_API_KEY=local-relayhub-key` 这个**假密钥**，不是真实签发的本地 key；`hermes.go:103` 的 `desired.Provider == "relayhub" || current["model"] == nil` 条件会在用户没有 `model` 段时**擅自改写默认 provider**，违反规格 §9.4"默认 Provider 变化必须在预览中明确显示"。

### P1-7 　导入/导出**没有接入 API**
`internal/export` 包同样零生产引用。`/api/v1/import-export` POST 恒 501，GET 返回 `supported:false`。UI 里甚至没有这个页面（规格 §10 要求"配置导入导出"菜单）。
```
PROBE9 POST /api/v1/import-export -> 501
```

### P1-8 　**没有任何接口可以签发 Gateway 本地 API Key**
`auth.LocalKeyService.Create()` 只在 `tests/e2e` 里被调用过。管理 API 没有 `/api/v1/keys` 之类的端点：
```
PROBE4 /api/v1/keys -> 404   /api/v1/local-keys -> 404   /api/v1/auth/keys -> 404   /api/v1/api-keys -> 404
```
用户现网库 `local_api_keys` 表 **0 行**。而 `gateway.go:150` 一旦 `cfg.Auth != nil` 就强制校验 —— **用户根本无法拿到能用的 key，Gateway 对真实客户端 100% 返回 401**。规格 §8.4 要求"Gateway 使用 RelayHub 本地 API Key……撤销本地 Key 必须能立即阻断"。

### P1-9 　签到适配器是**猜测的端点**，直接违反规格的 fail-closed 原则
五个适配器（`agentrouter`/`justdowork`/`gorouter`/`seekai`/`kktoken`）**代码几乎完全一样**，都硬编码 `POST {base}/api/user/checkin` + `GET {base}/api/user/self`，且都用 `Authorization: Bearer {channel.CredentialRef}` —— **注意这里传的是"凭据引用名"而不是凭据本身**（`agentrouter/adapter.go:46` 等），即适配器把 `cred-xxx` 这个 ID 当成密钥发出去了。

计划 Task 9 Step 1 明确要求："**为每个站点记录已验证的端点契约……If an endpoint cannot be verified, leave that operation disabled rather than guessing**"。仓库里**不存在任何 adapter contract 文档**，`internal/adapter/fixtures/` 只有 2 个文件（`builtin_responses.json` 358 字节、`example-provider.yaml` 206 字节），计划要求的 `*_responses.json`（每站点一份）不存在。五个适配器**一个单元测试都没有**（`find . -name '*_test.go'` 中没有任何 `adapter/{agentrouter,justdowork,gorouter,seekai,kktoken}` 测试）。

`gorouter.CheckIn()` 更离谱：它只是查了一次余额，然后**无条件返回 `Success: true`**（`gorouter/adapter.go:44-47`），谎报签到成功。
`agentrouter.CheckIn()` 在 HTTP 非 200 时回退到查余额，成功就报 `Success: true`（`agentrouter/adapter.go:66-73`）——同样是谎报。

---

## 4. P2 中等问题（11 条）

### P2-1 　`internal/config` 整个包是死代码
`prod_imports=0, test_imports=0`（除自身测试）。`cmd/relayhub/main.go` 只认 `-data-dir` 和 `-full-stack` 两个 flag，**端口无法配置**，`config.Load()` / `Defaults()` / `merge()` / `normalizeConfigPaths()` 共 163 行全部无人调用。规格 §4.1 要求"端口可配置；启动时检查冲突并明确报告"。实际表现见下：
```
relayhub: failed to create http proxy: listen tcp 127.0.0.1:8787: bind: address already in use
```
——报错信息尚可，但用户没有任何办法换端口。

### P2-2 　`web/` 与 `internal/web/` 两份完全相同的前端副本
`diff` 结果：`index.html`、`app.js`、`styles.css` 三个文件**逐字节相同**（各 18,960 / 47,158 / 20,255 字节）。`internal/web/embed.go` 从 `internal/web/` 嵌入，`Dockerfile:12` 却 `COPY web/ web/`（拷了不被嵌入的那份）。维护者改一份忘另一份 → 线上与源码不一致。**会话历史里已经踩过这个坑**（"子代理改了文件但二进制没重建，旧进程还在跑旧 CSS"）。
**修复**：删掉 `internal/web/` 的副本，改用 `//go:embed all:../../web` 或把 `web/` 移进 `internal/web/` 只保留一份，并在 Makefile/CI 中加防回归检查。

### P2-3 　Docker 镜像用 Go 1.24 构建，而 `go.mod` 要求 Go 1.27
`Dockerfile:2` `FROM golang:1.24-alpine`、`.github/workflows/test.yml:16` 和 `release.yml:15` 都是 `go-version: '1.24'`，但 `go.mod:3` 是 `go 1.27`。**CI 和 Docker 构建一定失败**（本机是 1.27.1 才能编译）。这意味着 `progress.md` 声称的"Task 13/15 完成并验证"**没有真正跑过 CI 或 docker build**。

### P2-4 　`go.mod` 所有依赖被标记为 `// indirect`
9 个直接使用的库（`uuid`、`go-toml/v2`、`yaml.v3`、`x/crypto`、`modernc.org/sqlite`）全部错标为 indirect。`go mod tidy` 从未正确执行过。

### P2-5 　Docker 健康检查端口硬编码，与可配置端口冲突
`docker/healthcheck.sh:5` 写死 `127.0.0.1:8790`；`docker/entrypoint.sh` 里的 `DATA_DIR` 变量赋值后**从未使用**（`mkdir -p "$DATA_DIR"` 之后直接 `exec` 传参）。

### P2-6 　`smoke-local.sh` 不是冒烟测试
计划 Task 12 Step 4 要求："脚本启动 Core……探测健康、测试 HTTP 和 SOCKS5、测试 Gateway 路由……任何失败退出非零"。实际实现（`scripts/smoke-local.sh`）只做了三件事：`go build`、`relayhub -version`、`go test ./internal/app ./tests/e2e`。**没有启动进程、没有探测端口、没有测代理、没有测 Gateway**。CI 把它当作质量门禁，等于没有门禁。

### P2-7 　`verify-release.sh` 的"密钥扫描"是摆设
只扫两个写死的字符串 `HERMES_CUSTOM_AXONHUB_API_KEY=ah-` 和 `top-secret-password-12345`。计划要求"absence of known secret patterns"。应改为正则族（`sk-`、`ghp_`、`AKIA`、`-----BEGIN .* PRIVATE KEY-----` 等）。同时没有校验 Docker 与 macOS 构建版本号一致（计划 Task 15 Step 1 明确要求）。

### P2-8 　`build-macos.sh` 产物名与文档/规格不符
脚本产出 `RelayHub-macOS-arm64.dmg` / `RelayHub-macOS-amd64.dmg`；`desktop/macos/README.md` 和计划 Task 14 写的是 `RelayHub-macOS-apple-silicon.dmg` / `RelayHub-macOS-intel.dmg`。`release.yml` 上传 `dist/*.dmg` 侥幸没炸，但文档对不上。

### P2-9 　macOS 壳存在两套并行实现，且都不管理 Core 生命周期
`AppDelegate.swift`（`@main`，Swift）与 `main.m`（Objective-C `main()`）**同时存在且互相冲突**；`build-macos.sh` 两个都不编译，只把 Go 二进制塞进 `.app/Contents/MacOS/relayhub`（即 `.app` 双击运行的是**无界面的 Go 进程**，CFBundleExecutable 写的却是 `RelayHub`，**这个可执行文件根本不存在** → `.app` 无法启动）。
计划 Task 14 要求壳能"启停 Core、菜单栏状态、登录项、通知"，实际两份代码**都只是打开 `http://127.0.0.1:8790` 的 WebView**，不启动也不停止 Core。
`KeychainStore.swift` 写得不错，但**同样不被编译、不被 Go 侧使用** —— `secrets.PlatformKeyProvider` 接口定义了，却没有任何 macOS 实现；`wiring.go:99` 无条件使用 `FileKeyProvider`（明文文件持有主密钥）。规格 §11.2 要求"macOS 优先使用 Keychain 保护主密钥"。
`Info.plist` 还开了 `NSAllowsArbitraryLoads: true`（全局关闭 ATS），应改为仅对 127.0.0.1 例外。

### P2-10 　`tests/macos/smoke.sh` 只检查文件存在，不检查架构、不启动
计划 Task 14 Step 1 要求"启动它、报告健康、干净停止、**使用预期架构**、不含凭据"。实际只 `[ -d ]` / `[ -x ]` / `[ -f ]` 三个判断。`progress.md` 却记为"verified (both arm64 and amd64 DMG packages built)"。

### P2-11 　前端 `detectProxies()` 逻辑错误且无意义
`app.js:130-148` 对 8 个常见端口发 `fetch(..., {method:"HEAD"})`：`try` 块里**无论响应是什么都 push**，`catch` 里只有 `AbortError` 才 push —— 由于跨源 `fetch` 必然抛 TypeError，实际行为是"只有超时的端口被认为开放"，与注释写的意图完全相反。且 `detectedProxies` 的结果在渲染代理下拉框时用法可疑。旁边还留着一段空注释块：
```js
if(navigator.userAgent){ // check clash default }   // 空实现
```

---

## 5. P3 轻微问题 / 死代码 / 冗余（8 条）

| # | 位置 | 问题 |
|---|---|---|
| P3-1 | `internal/repository/sqlite.go` | 整个文件只有 `SQLiteStore` 包装类型（7 行），全仓库无人使用 |
| P3-2 | `internal/storage/migrations.go` | 只有 `MigrateWithContext`（5 行），是 `Migrate` 的同义转发，无人调用 |
| P3-3 | `internal/gateway/transform.go:80` | `type UpstreamResponse = Response` 别名，无人使用 |
| P3-4 | `internal/catalog/service.go:58` | `validGroupStrategy` 只是 `validStrategy` 的别名，且创建 ModelGroup 时**没有校验 strategy**（只有 route 校验了） |
| P3-5 | `internal/api/server.go:1481-1485` | `safeProvider`/`safeChannel` 等 5 个函数全是恒等返回 `return p`，注释解释"凭据引用是安全的" —— 那就该删掉这层伪装，否则读者会误以为做了脱敏 |
| P3-6 | `internal/health/state.go` | `MarkDegraded`、`AllowProbe`、`RecordSuccess`、`ApplyRetryAfter`、`CooldownUntil`、`StatusIdle`、`StatusCooldown` 生产代码零引用 —— 熔断半开探测（`AllowProbe`）根本没接进 Gateway，`retryableStatus` 里 429 的 `Retry-After` 也没读（规格 §6.3 明确要求"429 遵循 Retry-After"） |
| P3-7 | `internal/domain/entities.go` | `ProxyURL`、`StreamPolicy`、`ManualModels`、`AutoSyncPattern`、`AccountRef`、`RequestTransform`、`ResponseTransform`、`RetryPolicy`、`FallbackPolicy`、`Capabilities`、`BaseURLTemplate`、`ProtocolRequirements` —— **12 个字段只在 domain/repository 里存取，业务逻辑零消费**。UI 采集了、数据库存了、然后没人用。其中 `CustomHeaders` 更严重：**Gateway 会读（`gateway.go:336`）但数据库根本没有这一列**（`002_channel_extensions.sql` 无 `custom_headers`，`channelColumns` 也没有），所以它永远是 nil |
| P3-8 | 仓库根目录 | 14 个 `task-N-report.md` 散落在项目根，与 `.superpowers/sdd/.../task-N-report.md` 重复；`IDEA.md` 只有 55 字节。属于开发过程产物，应移入 `.superpowers/` 或删除 |

**额外规范问题**
- `internal/secrets/keyring.go:24` `if filepath.IsAbs(p.Path) == false` —— 应为 `if !filepath.IsAbs(p.Path)`（`go vet` 不报，但违反 Go 规范）
- `internal/api/server.go:1391-1396` `requireID` 定义了却在 `validateProvider` 里改 `p.AdapterType`（值拷贝，改了没用，`server.go:1404-1406` 是无效代码）
- `internal/app/app.go:82` `a.listeners = 4` 硬编码魔数
- `internal/app/wiring.go:51-53` `stopCh`/`stopped`/`mu` 三个字段在 `Runtime` 里定义但从未使用
- `internal/api/server.go` 单文件 1,485 行，应按资源拆分
- `internal/logging/redact.go` 的 `apiPattern` 包含 `ah`（会误伤 "ah_" 开头的普通词），且不覆盖 `AKIA`/`glpat-` 等常见前缀

---

## 6. 与计划/规格的偏差核对表

| 计划任务 | 账本声称 | 实际状态 | 依据 |
|---|---|---|---|
| Task 5 管理 API + Web UI | complete | **部分** — settings/cli-sync/import-export 为 501 占位；无 key 管理端点 | P1-6/7/8 |
| Task 6 透明代理 | complete | **默认不可用** — 只允许 loopback | P1-3 |
| Task 7 OpenAI/Anthropic Gateway | complete | **协议转换未实现**；`/v1/models` 空；凭据未注入 | P0-3、P1-2、P1-4 |
| Task 9 五站点适配器 + 调度器 | complete | **端点全是猜的，无契约文档、无 fixture、无测试**；调度器三处崩溃/丢数据 | P0-6、P1-9 |
| Task 10 CLI 同步 | complete | **未接线**，UI 按钮假成功 | P1-6 |
| Task 11 `.rhx` 导入导出 | complete | **秘密区是占位符**，校验和不校验，未接线 | P0-7、P1-7 |
| Task 12 端到端接线 | complete | **快照不刷新**；e2e 测试自己绕过了 Bug | P0-1、P0-2 |
| Task 13 Docker | complete | **Go 版本不匹配，构建必失败** | P2-3 |
| Task 14 macOS | complete + "verified" | **`.app` 无法启动**（CFBundleExecutable 指向不存在的文件）；Keychain 未接线 | P2-9、P2-10 |
| Task 15 CI/发布门禁 | complete + "verified" | CI 用错 Go 版本；smoke 脚本不 smoke；密钥扫描形同虚设 | P2-3、P2-6、P2-7 |

**最终验收清单（计划第 736-753 行）实测结果**：16 条中至少 **11 条不成立**。

---

## 7. 建议修复顺序

**第一批（必须一起修，否则无法验证任何东西）**
1. P0-5 `logging.New(nil)` panic（最小改动，先让错误路径不崩）
2. P0-1 协议命名统一
3. P0-2 路由快照热刷新 + **重写 e2e 测试，移除手工回填**
4. P0-3 上游凭据注入
5. P1-8 本地 API Key 签发端点
> 完成后应能做到：界面上加一个渠道 → 不重启 → 用签发的 key 调 `/v1/chat/completions` → 真实上游返回内容。**这是整个项目的最小可用闭环，现在完全不成立。**

**第二批（安全与数据完整性）**
6. P0-4 CSRF 防护
7. P0-6 调度器三处缺陷
8. P0-7 导出秘密区 + 校验和 + 导入回滚

**第三批（功能补齐）**
9. P1-1 用量记录接线 → P1-2 `/v1/models` → P1-3 代理策略接线 + P2-1 config 接线 → P1-5 ModelGroup Members → P1-4 协议转换 → P1-6/7 CLI 同步与导入导出接线 → P1-9 适配器契约（**先写契约文档，无法验证的端点保持 disabled，不要继续猜**）

**第四批（工程质量）**
10. P2-3/2-4 Go 版本与依赖 → P2-2 前端单一来源 → P2-6/2-7 真正的 smoke/release 门禁 → P2-9/2-10 macOS 壳 → P3 全部清理

**最后**：更新 `.superpowers/sdd/2026-09-12-relayhub-implementation/progress.md`，把"15/15 完成、Deferred: None"改成真实状态。**账本失真是本次审计发现的最深层问题** —— 它让后续所有人基于错误前提工作。

---

## 8. 审计过程的可复现性

所有 P0/P1 结论由临时探针测试在真实环境跑出，审计结束后探针已删除，仓库回到原状：

```
go build ./...   → OK
go test ./...    → 全部包 ok（含 tests/e2e）
git status       → 与审计前一致（33 untracked，无新增/修改）
```

需要复现时，探针覆盖的断言点是：协议不匹配、快照不刷新、凭据未注入、nil logger panic、无 key 端点、代理策略拒绝公网、usage 零记录、`/v1/models` 空、CSRF 跨站写入、Anthropic↔OpenAI 未转换、签到记录主键冲突、退避不可取消、调度器重启 panic、导出占位符、校验和不校验。

