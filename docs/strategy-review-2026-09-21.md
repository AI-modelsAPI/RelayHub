# RelayHub 战略评审与差异化路线（2026-09-21）

> 评审方式：通读 `internal/`（22.7k 行 Go）、`web/`、全部 docs 与审计报告；用 GitHub / 官方 README 核对 2026-09 竞品实况。
> 本文**取代** `docs/ROADMAP-2026.md`（及已删除的 `docs/product-roadmap.md`）中互相矛盾的部分，并**终止** AxonHub 复刻计划（`.superpowers/axonhub-replication-plan.md`，已随 2026-09-21 主干快照一并删除）。
>
> **版本说明**：§1–§3 的代码实证基于 2026-09-20 交接快照（`4bc94e6`）。2026-09-21 主干快照（另一条并行开发线：identity / AIMD / verify / lab / MCP / 桌面范式 UI）在其中若干点上已有部分动作，§6.3 记录了两条线的合并结果。
> 后半部分是"决策记录"与"实施清单"，对应本次提交实际落地的代码。

---

## 0. 一句话结论

RelayHub 的架构骨架是对的（六层资源模型、fail-closed、密钥不出本机、单二进制），但项目文档里认定的护城河——"自动签到 × 智能路由"——**在 2026 年已经不是独有的了**。真正还剩下、且别人难以复制的是三样东西：

1. **零依赖单二进制的本地守护进程形态**（Go + SQLite，无 Node / Postgres / Redis）；
2. **真实浏览器 + fail-closed 的签到合规立场**（Turnstile 站只能靠真浏览器，且绝不本地伪造"已签"）；
3. **透明代理与 AI 网关同进程的"统一出口"**（`IDEA.md` 的后半句）。

而这三样在评审时都是半成品，代码里存在多处"策略存在但没有数据喂"的空转。

---

## 1. 竞品实况（2026-09）

`docs/product-roadmap.md` 对标的是 LiteLLM / Bifrost / AxonHub / New API / CC Switch；`ROADMAP-2026.md` 据此断言"签到+路由是别人没有的"。**这个判断已经过时**，漏掉了两个最直接的对手：

| 竞品 | 形态 | 多站聚合 | 自动签到 | 余额/额度感知路由 | 浏览器过 Turnstile | CLI 接管 | 透明代理 | 通知 | 热度 |
|---|---|---|---|---|---|---|---|---|---|
| **Metapi**（2026-02 起） | Node/Fastify + React，Docker/桌面包 | ✅ New API / One API / OneHub / DoneHub / Veloera / AnyRouter / Sub2API | ✅ cron + 奖励解析 + 并发锁 | ✅ 成本 40% / 余额 30% / 使用率 30% 加权，决策可解释 | ❌ 纯 HTTP | 部分（OAuth Codex / Claude / Gemini） | ❌ | ✅ 5 种 | 3.3k★ |
| **All-API-Hub** | 浏览器扩展 | ✅ | ✅ | ❌（不做网关） | 天然在浏览器里 | ❌ | ❌ | 部分 | Chrome 商店上架 |
| **CLIProxyAPI** + 生态（Quotio / ProxyPal / CodMate…） | Go 单二进制 | 官方订阅 OAuth 为主 | ❌ | 多账号轮询 | ❌ | ✅ 很强 | ❌ | — | 大量衍生项目 |
| **CC Switch** | Tauri 桌面 | 50+ 预设 | ❌ | 故障转移 + 熔断 | ❌ | ✅ 9 个工具 + MCP/Skills/Prompts 同步 + 云同步 | ❌ | — | 成熟 |
| **RelayHub**（评审时） | Go 单二进制 + SQLite | 5 内置 + generic + declarative | ✅（浏览器路径半成品） | 策略存在，**无数据源** | ✅ CDP，fail-closed | 3 个 CLI，预览/备份/回滚做得比多数人认真 | ✅ HTTP + SOCKS5 | ❌ | 0★ |

来源：Metapi 官方 README（自动签到、余额管理、四级成本信号智能路由、五种通知渠道、OAuth 连接 Codex/Claude/Gemini CLI）；All-API-Hub 项目页；CLIProxyAPI README "Who is with us" 列表；CC Switch 官方 README。

**结论**：在"签到""路由""CLI 接管"三个单项上，RelayHub 每一项都落后于该项的领先者。它唯一独占的交集是 **"单二进制 × 真实浏览器签到 × 透明代理 × 本地优先"**——差异化必须从这个交集里长出来，而不是在单项上追赶。

---

## 2. 用户与场景：三个场景，只有一个是独占的

| 场景 | 用户画像 | 谁在抢 | RelayHub 胜算 |
|---|---|---|---|
| **A. "一个本地入口跑所有 AI CLI"** | 用 Claude Code / Codex / Hermes 的个人开发者，手里有若干中转站 Key + 官方 Key | CC Switch、CLIProxyAPI、Metapi 三方混战 | 低——功能面被碾压，只能靠"更轻、更安全"做补充 |
| **B. "公益站免费额度农场化"** | 多站多账号，每日签到，遇 Turnstile 要真浏览器 | Metapi、All-API-Hub、自家 autosign | 中——**真实浏览器 + 每账号独立 profile + fail-closed** 是 Metapi 做不了的 |
| **C. "所有 AI 出站流量统一出口、统一审计、密钥不出本机"** | 在意网络环境 / 隐私 / 合规的用户；需要按渠道走不同出口（住宅代理、机场节点）抗 429 / 封号的人 | **没有人在做** | 高——这正是 `IDEA.md` 的后半句，但代码里没落地 |

**场景 C 是被所有竞品忽视、又与 RelayHub 现有能力（proxy 包、secrets、audit、redaction）天然吻合的方向。**

---

## 3. 代码现实：评审新发现（AUDIT-REPORT 未覆盖）

### 3.1 三个"智能路由"策略在生产中全部空转
- `quota-aware` 策略读取 `health.State.QuotaRemaining / QuotaScore / QuotaResetAt`，但**全仓库没有任何写入点**；`adapter.Balance()` 在生产代码零调用；调度器签到成功后不回写额度。
- `latency` / `success-rate` 策略的输入来自 `RecordSuccess(id, 0, 1)`——**延迟恒为 0、成功率恒为 1**。
- `record()` 的计时起点放在**上游已返回之后**，量到的是写回客户端的时间，不是上游耗时。
- 只有"导致重试"的失败才被 `RecordFailure`；最后一次尝试失败（5xx / 网络错误）**不计入健康状态**。
- 结果：三个高级策略全部退化为 `priority + ProviderModel.ID 字典序`。

### 3.2 IDEA 的后半句没落地：没有"按渠道出口"
- `Channel.ProxyURL` 字段存在、入库、UI 采集，但 `HTTPUpstream` 用 `http.DefaultClient`，adapter 的 HTTP client 也不读它，CDP 启动参数没有 `--proxy-server`。
- 自带的 8787/8788 透明代理只是个"旁路服务"，**AI 网关和签到本身并不经它出站**。autosign 已验证的 `switchToBackupProxy`（429 自动换出口）也没移植。

### 3.3 CDP 签到的"证据链"不成立
- `executor.go` 用的是通用选择器（`#checkin-btn, .checkin-btn, .checkin-success`），真实 new-api 系前端基本命不中。
- 成功判据是 **DOM 元素可见**，而不是服务端记录。这直接违反 `BROWSER-PLAN.md` 验收第 5 条——而纯 HTTP 路径里已经有 `GET /api/user/checkin?month=` 的服务端判据，CDP 路径却没有复用。

### 3.4 其他
- 健康状态只在内存，重启即丢；熔断开路冷却写死 `1s`；**不读 429 的 `Retry-After`**（规格 §6.3 明确要求）。
- 无任何通知能力；"签到失败 / 额度告急 / 渠道掉线"用户无法感知——农场无人值守就无从谈起。
- `web/` 与 `internal/web/` 双份靠 `make check-web` 守着。
- `.superpowers/axonhub-replication-plan.md`（继续复刻 AxonHub）与 `ROADMAP-2026.md`（停止复刻）**互相矛盾**；`docs/acceptance/` 下 15 份交接文档说明流程负债已大于代码负债。

### 3.5 做得好的（保留并对外讲）
六层资源模型的正交性、`ResolveExcluding` 首字节前故障切换、跨协议转换（OpenAI⇄Anthropic 双向）、`Decision.Excluded` 记录每个候选被排除的原因、adapter 契约文档 + 五站 fixture 单测、CLI 同步的预览/备份/回读/回滚、usage 只存元数据不存 prompt、`.rhx` Argon2id+AES-GCM 导出。**这些是"可信赖的本地基础设施"的底子。**

---

## 4. 差异化路线：先选一个身份

| 路线 | 定位 | 对手 | 决定 |
|---|---|---|---|
| ① **本地 AI 出口守护进程**（Local AI Egress Daemon） | 本机所有 AI 流量的唯一出口：路由、凭据、出口代理、脱敏、审计在一个进程 | 无人正面竞争 | **作为身份** |
| ② 公益站保活农场 | 真浏览器签到 + 额度驱动路由 | Metapi、All-API-Hub | **作为①里的杀手级功能**，不作为身份 |
| ③ CLI 接管中枢 | 一键配置所有 CLI | CC Switch、CLIProxyAPI | 维持"3 个 CLI 做到最稳"即可，**不扩面** |

选①的理由：它把 RelayHub 已有但零散的独特件（透明代理、secrets、audit、redaction、单二进制、菜单栏）串成一个别人讲不出的故事——**"密钥不出本机、流量一处出口、每一跳可解释"**。②③都是别人已占的战场。

---

## 5. 决策记录（本次评审代用户做出的决定）

| # | 决定 | 理由 |
|---|---|---|
| D1 | 产品身份定为 **Local AI Egress Daemon**；README 首屏与 `docs/architecture.md` 按此措辞 | §4 |
| D2 | **终止** AxonHub 像素级复刻计划；`.superpowers/axonhub-replication-plan.md`、`docs/axonhub-gap-ledger.md`、`docs/product-roadmap.md` 与 `docs/acceptance/` 交接文档已在主干删除，不再恢复 | 通用 CRUD 卷不过 One-API 系；ROADMAP-2026 已提出但未执行 |
| D3 | M3"多节点农场"**无限期推迟**；单机闭环成立前不做分布式 | 过早分布式只会放大空转 |
| D4 | 路由策略命名对齐 M1 文档：新增 `quota-first`（= `quota-aware` / `quota_aware` 别名，跨 tier 按已观测余额排序，余额归零者排除并给出 `quota exhausted` 理由）。`cost-first` **推迟到 P1**：目前 `InputPrice/OutputPrice` 只是录入值、无实测口径，价格为 0 会被误判为免费，先不上线 | 避免"未定价 = 免费"误判；先让唯一可靠的信号（余额）进入路由 |
| D5 | 额度以 **USD** 作为跨站统一单位（new-api 系 `quota / quota_per_unit`），`Channel.QuotaState` 改为结构化 JSON，旧自由字符串容忍不报错 | 各站 quota 单位不同，USD 是唯一可比口径 |
| D6 | 出口优先级：`Channel.ProxyURL` > 全局 `egress_proxy_url`（config / `RELAYHUB_EGRESS_PROXY`）> 环境变量 `HTTPS_PROXY` > 直连；网关、签到 adapter、CDP 浏览器三条出站路径**同源** | "统一出口"必须覆盖全部出站路径才成立 |
| D7 | CDP 路径成功判据改为 **服务端确认**（`CheckinStatus` / `TodayBonus`）；DOM 成功仅作为"可以去验证"的信号；服务端无记录 → `failed`，绝不 `success` | BROWSER-PLAN 验收第 5 条 |
| D8 | 通知先做 Webhook + Bark + Telegram 三种，配置走 config 文件 / 环境变量（token 不入库明文）；每 (事件, 渠道) 5 分钟冷却 | 与 Metapi 追平"无人值守"的最小集 |
| D9 | 健康状态变迁（→ 熔断 / → 恢复 / → 额度耗尽）落 `health_records`；每请求不落库 | 可观测且不放大写入 |
| D10 | 不引入新的运行时依赖；全部用标准库实现 | 保持"单二进制、干净依赖"的差异点 |

---

## 6. 实施清单（本次提交）

| 批次 | 内容 | 涉及包 | 验收 |
|---|---|---|---|
| **P0-A 健康数据求真** | 上游延迟从调用前计时；成功率用滑动窗口（最近 50 次）；最后一次失败也计入；429 读取 `Retry-After` 进入冷却；熔断冷却随连续失败指数增长（1s→…→5min）；状态变迁落库 | `health`, `gateway`, `app` | `latency` / `success-rate` 策略在两渠道对照测试中选出真实更优者 |
| **P0-B 额度成为路由信号** | 签到成功 / 已签后调用 `Balance()`；每小时余额轮询（不支持的 adapter 24h 停靠）；写结构化 `QuotaState` + `health` 额度字段；`quota-first` 策略（`cost-first` 推迟，见 D4） | `checkin`, `adapter`, `domain`, `health`, `router`, `app` | 同模型两渠道，有额度者被 `quota-first` 选中；额度归零后被排除并给出理由 |
| **P0-C 按渠道出口** | `internal/egress`：按 ProxyURL 缓存 `http.Client`；网关 / adapter / CDP 三路同源；全局默认出口配置 | `egress`, `gateway`, `adapter`, `browser`, `config`, `app` | 网关请求经指定代理出站（测试用本地代理探针断言 CONNECT 命中） |
| **P0-D CDP 证据链** | CDP 成功后服务端确认；选择器可按 Provider 配置覆盖 | `checkin`, `adapter`, `browser` | 模拟 CDP 报成功但服务端无记录 → 记录为 `failed` |
| **P0-E 通知** | `internal/notify`：Webhook / Bark / Telegram；事件：签到失败、需人工、额度低于阈值、熔断开路、余额刷新失败；冷却 | `notify`, `checkin`, `health`, `config`, `app` | 事件触发一次且冷却期内不重复 |
| 文档 | 本文；README / architecture 措辞；旧计划标记 superseded | docs | — |

### 6.1 明确不做（本次）
- 429 自动换备用出口（需要备用出口列表 UI，放到 P1）。
- `cost-first` 策略（见 D4，放到 P1 与"今日省下 $Y"一起做，先解决价格口径）。
- 通知的 Web UI 配置页（先 config / env）。
- 多节点、多租户、计费。

### 6.2 实施结果（2026-09-21，分支 `arena/01a0c0fa-relayhub`，以单个提交合入 2026-09-21 主干快照）

| 批次 | 关键改动 | 回归测试（RED → GREEN） |
|---|---|---|
| P0-A | 网关在**发请求前**计时（TTFB）；成功率改为最近 50 次滑动窗口且最后一次失败计入；`Retry-After` 解析进入冷却；熔断冷却随连续失败指数增长（1s→5min）；状态变迁经 `SetObserver` 落 `health_records` | `internal/gateway/health_signal_test.go`、`internal/health/*_test.go`、`internal/app/health_persist_test.go` |
| P0-B | `domain.QuotaSnapshot`（USD 口径，`Channel.QuotaState` 结构化 JSON，旧文本容忍）；签到成功 / 已签后立即 `Balance()`；每小时余额轮询；`health.SetQuota`；`quota-first` 跨 tier 排序；启动时从库中快照回填（≤48h）；API `stats.quota` / `stats.live`；表单保存不覆盖系统字段 | `internal/checkin/balance_test.go`、`internal/router` `TestResolveQuotaFirstUsesObservedBalance`、`internal/api/channel_quota_test.go` |
| P0-C | `internal/egress`：按 (proxy, timeout) 缓存 `http.Client`；`Channel.ProxyURL` > `egress_proxy_url` > 环境变量；`direct` 显式直连；非法代理**拒绝出站**而不是换路；网关 `HTTPUpstream.ClientFor`；adapter `ClientProvider`（Base / Generic / Declarative 全部接入）；浏览器 `--proxy-server` / `--no-proxy-server`（带凭据的代理拒绝启动）；`config.json` + `RELAYHUB_EGRESS_PROXY` | `internal/egress/egress_test.go`（本地录制代理断言 absolute-URI 与 CONNECT 均经代理）、`internal/gateway/egress_test.go`、`internal/adapter/egress_test.go`、`internal/browser/launch_test.go`、`internal/checkin/scheduler_egress_test.go` |
| P0-D | `adapter.CheckinVerifier`；`BaseNewAPIAdapter.VerifyCheckin` 复用签到日历 / 奖励日志 / 用户标记；CDP 报成功后必须服务端确认：有记录 → `success`（奖励取服务端值）、无记录 → `failed`、无法核验 → `need_manual`；无 verifier 的 adapter 记录显式 `unverified`；选择器可由 `Provider.Capabilities` JSON 覆盖 | `internal/checkin/cdp_verify_test.go`、`internal/adapter/verify_test.go`、`internal/browser/launch_test.go` |
| P0-E | `internal/notify`：Webhook / Bark / Telegram；异步分发、每 (事件, 渠道) 5 分钟冷却、队列有界、未配置时零开销；事件：`checkin_failed`、`checkin_need_manual`、`balance_refresh_failed`（每次故障只报一次）、`circuit_open`、`auth_expired`、`quota_exhausted`、`channel_recovered`、`quota_low`（阈值默认 $0.50）；`POST /api/v1/notify/test`；`settings.notify` / `settings.egress` / `stats.egress` 可解释 | `internal/notify/notify_test.go`、`internal/checkin/events_test.go`、`internal/app/notify_wiring_test.go`、`internal/api/egress_notify_test.go` |

运维入口见 [`docs/operations.md`](operations.md)（出口与通知配置、验证方法）。

### 6.3 与 2026-09-21 主干快照的合并说明

主干快照（`docs/dev/STATUS-2026-09-21.md`）在沙箱里未能用 Go 1.27 跑测试；本分支在 Go 1.27.1 上对合并结果跑通了 `gofmt` / `go vet` / `go test -race ./...`（仅 `tests/e2e/TestProxyDefaultAllowsPublicTarget` 因沙箱无外网失败，与代码无关）。重叠点的取舍：

| 主题 | 主干快照的做法 | 合并后 |
|---|---|---|
| 渠道代理 | 网关每次请求临时 `&http.Transport{Proxy}`；adapter 走 `identity.HTTPClient`（仅 base 的两处调用） | 统一为 `internal/egress`：按 (代理, 超时) 缓存 Transport，覆盖网关 / 全部 adapter / 浏览器；未接线时仍尊重 `Channel.ProxyURL`；`identity.HTTPClient` 仅保留给出口探针 |
| `Retry-After` | 解析后 429 且 ≤2s 时原地等待重试 | 保留原地等待；同时把 `Retry-After` 写入健康冷却，且每次尝试（含最后一次）都计入成功率 / 延迟 |
| 401 | `DisableKey` 禁用对应 Channel Key | 保留，并作为一次失败记入健康，不再盲目重试 |
| AIMD 限速 / 粘性会话 / verify / lab / MCP | 新增 | 原样保留，接线不变 |
| 成功路径健康记录 | 仍是 `RecordSuccess(id, 0, 1)` 常量，延迟从写回开始计时 | 改为 `RecordOutcome(id, true, TTFB)`，延迟从发请求前计时 |
| 文档 | 删除 AUDIT / acceptance / product-roadmap / AxonHub 计划 | 跟随删除；本文与 `ROADMAP-2026.md`、`PLAN-2026-09-20.md`、`proposals/2026-09-20-differentiation.md` 并存，冲突处以本文的 P0 决策为准 |

---

## 7. 后续（P1 / P2，按优先级）

1. **一行安装 + 30 秒首跑**：`brew tap` / `curl | sh`；首跑向导"粘贴站点 URL + Key → 自动识别 new-api 系 → 拉模型 → 一键同步 Claude Code"。
2. **`relayhub route explain --model X`**：把 `Decision.Excluded` 打印成决策树；顺带 `status / checkin / key create` 做成真 CLI。
3. **首页一个数字**："今日免费额度 $X / 今日省下 $Y"。
4. **出口探针**：每渠道显示"当前出口 IP / 地区 / 上次 429 时间"；429 自动换备用出口。
5. **声明式站点 DSL 扩展**：checkin / balance / models / login 判据 / Turnstile 选择器都在 YAML 里；`relayhub adapter test site.yaml`。
6. **隐私可验证**：一键导出诊断包，证明不含 prompt / 密钥。
7. `web/` 单一来源（`//go:embed` 直接指向一个目录）；`docs/acceptance/` 合并为一份 `STATUS.md`。

---

## 8. 风险提示

- **合规边界是资产**：只操作用户自有账号、不绕过验证码、不伪造指纹——写进 README 首屏。**不要**把 Claude 订阅 OAuth 暴露给其他客户端（违反 ToS）。
- **公益站生态高波动**：身份不能建立在"五个站"上；声明式 DSL + 服务端判据是对冲手段。
- **"绿灯假象"曾经发生过**：保留 RED→GREEN→真实运行的纪律，但验收从"文档勾选"改为一个可重复跑的端到端 smoke。
