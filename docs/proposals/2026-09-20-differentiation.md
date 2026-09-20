# RelayHub 差异化特性提案（基于代码实读）

> 日期：2026-09-20 · 依据：通读 `README / IDEA / architecture / product-roadmap / ROADMAP-2026 / docs/dev/M1–M4 / 设计 spec` 与 `internal/{gateway,router,adapter,checkin,browser,clisync,proxy,usage,app,domain}` 源码。
> 本文**只提既有路线图里没有的东西**。已排除（因为你已经规划了）：额度感知路由、签到自愈、成本路由、免费额度账本、成本面板、健康时间线、多节点农场、签到 DSL、CLI 用量洞察、请求追踪、WS 推送、流内续传、零信任凭据隔离。

---

## 0. 一个重新定位：把"上游不可信"当作产品前提

LiteLLM / Bifrost / AxonHub / New API 全部默认**上游是诚实的一方 API**。RelayHub 的上游是公益站/中转站——**波动、限额、随时跑路、而且可能不诚实**（掉包模型、假流式、截上下文、注入提示词、吞掉 prompt cache）。

RelayHub 还有一个所有竞品都没有的位置优势：它同时站在三个点上——

1. **用真实浏览器登录**这些站（`internal/browser` + `internal/checkin`）
2. **用 API 消费**这些站（`internal/gateway`）
3. **接管本机编程 Agent 的配置**（`internal/clisync`）

竞品只做第 2 点。下面每一条特性都建立在"上游不可信"这个前提，以及 1+3 的位置优势上。这套东西合在一起可以讲成一个别人抄不走的故事：

> **RelayHub 是唯一一个"替用户防着上游"的 AI 网关。**

---

## 1. 提案清单（按差异化强度排序）

### A. 中转站真伪审计（Relay Truth Audit）★★★

**痛点**：公益站常见套路——`claude-sonnet-4` 名义下实际给的是便宜模型；号称 200k 上下文实际 32k 就截断；假流式（憋完整段一次吐出）；偷偷注入自己的 system prompt / 广告；把 `cache_control` 丢掉按全价计费。用户完全无法分辨，路由器把它们一视同仁。

**功能**：

- **主动探针**（复用 `checkin.Scheduler` 的调度节奏，低成本、可设频率，按 渠道×模型 执行）：
  - *身份指纹探针*：一小组能区分模型家族的提示（tokenizer 怪癖、知识截止日期事实、拒答风格、固定格式习惯），与"可信渠道"上采集的参考指纹比对；
  - *上下文真实性探针*：needle-in-haystack 在 32k / 100k / 200k 位置各放一次，得出**实测上下文长度**，与 `Model.ContextWindow` 对比；
  - *系统提示完整性探针*：要求模型逐字复述 system prompt，检测是否被注入；
  - *流式真实性*：统计 chunk 间隔分布——假流式的特征是长 TTFT 后一次性爆发。
- **被动信号**（每个真实请求免费获得）：TTFT、tokens/s 分布、`finish_reason` 异常、输出截断（无 `message_stop` / `[DONE]`）、响应体 `model` 字段与请求不符、`usage` 可信度（cache tokens 永远为 0、prompt_tokens 与本地估算偏差 >20%）。
- **产出**：每渠道一个 **Trust Score (0–100)** 附证据链；"疑似掉包"告警；"实测上下文 ✔" 徽章；Trust Score 作为路由的硬过滤或权重因子。

**为什么独一无二**：没有任何网关审计上游。独立的"模型真伪测试脚本"存在，但没有一个和路由闭环。

**代码锚点**：新包 `internal/verify`（注意 `internal/audit` 已被审计日志占用）；`HealthRecord` 旁新增 `probe_records` 表（migration 003）；`router/selector.go` 增加 trust 因子；`transform.go:652 writeSSE` 已经把整条流缓存在 `captured` 里，顺手就能算时序与完整性；`recordFor` 给 `RequestRecord` 加 `ttft_ms / upstream_model / finish_reason / cache_read_tokens`。

**工作量**：M（先做被动信号 1 周，再做 3 种主动探针 2 周）。

---

### B. 会话粘性 + Prompt Cache 亲和路由（Cache-Affinity Routing）★★★

**痛点**：Claude Code / Codex 每一轮都带着几万 token 的重复前缀。Anthropic/OpenAI 对命中缓存的前缀打 1 折并显著缩短 TTFT——**前提是连续请求落在同一个上游账号（通常 = 同一个 key）且在缓存 TTL（~5 分钟）之内**。

而现在：`app/wiring.go:321 resolveChannelCredential` 用一个全局 `atomic.Uint64` 对启用的 key **逐请求轮转**，加上跨渠道 weighted/RR，一个 Claude Code 会话的相邻两轮几乎必然打到不同账号 → 缓存全 miss，成本 5–10×，TTFT 变长，而用户毫无感知。

**功能**：

- 派生 **Session Key**：优先用 Claude Code 自带的 `metadata.user_id`，其次 hash(system prompt + 首条 user message)，也接受 `X-Session-Id` 头；
- (session → channel + key) **粘性绑定**，滑动 TTL 对齐上游缓存 TTL（Anthropic 5 min，可按 Provider 配置）；仅在失败/额度耗尽时解绑；
- **缓存感知的故障转移**：被迫迁移时优先选"最近服务过同一前缀 hash"的渠道（温缓存）；
- 跨协议转换保留 `cache_control` 块、透传 `anthropic-beta` 头（grep 确认目前完全没有处理）；
- **可观测**：从 `usage.cache_read_input_tokens` / `prompt_tokens_details.cached_tokens` 算出每渠道**缓存命中率**——很多中转站根本不透传缓存、照全价收费，这本身就是 A 的一条审计信号。

**为什么独一无二**：服务端网关的 session affinity 是泛化的负载均衡概念，不感知缓存 TTL；本地工具（cc-switch 等）根本没有会话概念。而且收益可以直接换算成 M2 账本里的"今日省下 $N"。

**代码锚点**：`router.Request` 加 `SessionKey`；`Resolver` 持有带过期的亲和表；`resolveChannelCredential` 改为接受 session 提示而不是盲目轮转；`gateway.requestHeaders` 保留 beta 头；`RequestRecord` 加 cache 字段。

**工作量**：S–M（1–2 周）。**ROI 最高，且是 E 的前置。**

---

### C. 身份一致性护栏：签到与调用同出口（Identity Bundle）★★☆

**痛点**：中转站的风控会看"浏览器登录时的 IP / UA / 时区"和"API 调用时的 IP / UA"是否一致，不一致就封号。RelayHub 是唯一同时控制两端的工具——但 `domain/entities.go:45 Channel.ProxyURL` 存了却**没有任何地方使用**（gateway / checkin / browser 均无引用，`HTTPUpstream.Client` 为 nil 时直接走 `http.DefaultClient`），UA 也只能靠手填 `CustomHeaders`。

**功能**：

- 每渠道一个 **Identity Bundle** = 出口代理 + UA + Accept-Language + 时区 + 浏览器 profile；
- 四处统一强制使用：① CDP 签到浏览器（`--proxy-server`、profile）、② 声明式/API 签到的 HTTP client（`adapter/base.go`）、③ 网关上游调用（按 Decision 选 `http.Transport`）、④ 健康探测；
- **漂移检测**：定期经该代理查出口 IP，一旦变化就暂停该渠道签到并告警（避免"换了 IP 还在签到"触发风控）；
- 农场模式："一个账号一个身份"的批量配置模板。

**为什么独一无二**：指纹浏览器不做 API 路由，API 网关不做浏览器签到；只有 RelayHub 两边都在手里。

**工作量**：S（1 周）。

---

### D. 对冲请求与首字节竞速（Hedged Requests）★★☆

**痛点**：公益站延迟是肥尾分布——经常卡 20–60 秒才出第一个字节。现有故障转移只在**出错**时触发，对"慢但没错"无能为力。

**功能**：

- 路由标记 `hedge: true`：先打主渠道；若在 `max(3s, 该渠道 p90 TTFT)` 内没有首字节，向下一候选再发一份；**谁先出首字节谁赢**，另一个 `ctx.Cancel()`；
- 预算护栏：仅当两边都有免费额度 / 成本低于阈值时才对冲；对冲比例上限（如 ≤10% 请求）避免翻倍消耗；
- 交互模式可选"首 token 竞速"，专为 Claude Code 这类对 TTFT 敏感的场景。

**为什么独一无二**：Google 级 RPC 的标准手段（Dean & Barroso, *The Tail at Scale*），但没有任何面向个人的 AI 网关提供；而且 RelayHub 用户手里的"免费额度多、时间宝贵"让这个权衡格外划算。

**代码锚点**：`gateway.go` 的 attempt 循环改为 errgroup 双发；`writeSSE` 需改成从"胜者"读；依赖 A 的被动 TTFT 统计。

**工作量**：M。

---

### E. Agent 失控熔断（Runaway Agent Guard）★★☆

**痛点**：编程 Agent 会死循环——同一个工具调用重复 50 次、同一个报错反复重试，凌晨三点把一天的额度烧光。网关只看 token 不看意图；但有了 B 的 Session Key，RelayHub 能看到**同一会话的每一轮**。

**功能**：

- 每会话滑动窗口检测：① 相邻请求近似重复（对末尾 N 条消息做 simhash）、② 完全相同的 `tool_calls`（名称+参数）连续出现、③ 每会话 tokens/min 或 $/h 上限、④ 回合数上限；
- 动作分级：软（可选注入一句 system 提示"你似乎在循环"）、硬（返回带清晰说明的 429，Claude Code 会原样展示）、通知（菜单栏 / WS）；
- 借 `auth/local_keys` 给本地虚拟 Key 加 **scope 与预算**，实现"每个项目一个 Key、各自限额"。

**为什么独一无二**：LiteLLM 有按 Key 的预算，但没有语义级循环检测；没有任何网关在协议层做"Agent 失控保护"。

**工作量**：M（依赖 B）。

---

### F. 请求回放实验室（Replay Lab / 渠道擂台）★★

**痛点**："哪个公益站对*我的*工作负载最好？"——现在全靠感觉。调试时："Claude Code 刚才那个奇怪回答，是模型的问题还是中转站的问题？"——无法复现。

**功能**：

- **可选、默认关闭**的加密请求捕获（环形缓冲区，走现有 `secrets` 的 AES-GCM），与项目"不留存 prompt"的立场一致；
- 任选一条捕获请求 → 扇出到 N 个渠道 → 并排对比输出 diff、TTFT、tok/s、计费 token、成本；一键"把胜者设为最高优先级"；
- **回归回放**：保存 20 条黄金请求，中转站一有风吹草动就重放一遍，与存档输出比对漂移——为 A 的 Trust Score 供数据。

**为什么独一无二**：promptfoo 之类是评测工具，不在网关里、没有实时渠道数据；没有任何聚合器有这个。

**工作量**：M。

---

### G. 自适应限流学习与准入排队（Learned Rate Limits）★★

**痛点**：公益站的 RPM/TPM 没文档、很紧。现在 `gateway.go:385 retryableStatus` 对 429 **立即换渠道**——白白烧掉另一条渠道的额度；`Retry-After` / `x-ratelimit-*` 头完全没读。

**功能**：

- 按 渠道×Key 维护令牌桶，参数用 AIMD 从 429 + `Retry-After` + `x-ratelimit-*` 学出来；
- 当桶即将回填（≤2s）时**本地短排队**而非立即转移；每 Key 并发上限；
- UI 展示"学到的限额"；与 B 联动：瞬时 429 不应打断会话粘性。

**为什么差异化**：New API 是静态权重，LiteLLM 的 RPM/TPM 要手填；"自动学 + 排队"对无文档的免费站是刚需。

**工作量**：S–M。

---

### H. 网关即 MCP 服务（Self-Managing Gateway）★★

**功能**：把控制面暴露为 MCP server（stdio + streamable HTTP）：`list_channels` / `quota_status` / `switch_route` / `run_checkin` / `explain_last_request`（为什么选了渠道 X）/ `trust_report`。由 `clisync` 自动写进 Claude Code / Codex 的 `mcpServers`。于是用户可以直接在 Claude Code 里问"今天为什么这么慢？"或者"额度不够就自己换个渠道"。

**为什么独一无二**：没有网关把自己暴露给它所服务的 Agent；这正好复用 clisync 这条护城河，且对 Agent 用户群极具吸引力。

**工作量**：S（复用 `api/server.go` 现有 handler，薄薄一层 JSON-RPC）。

---

### I. AI 流量透明劫持（可选实验）★

现有 HTTP/SOCKS5 代理按 spec 8.1 "不参与 AI 账号路由"。可增加**可选模式**：对白名单域名（`api.openai.com` / `api.anthropic.com` / `generativelanguage.googleapis.com`）用本地生成的 CA（私钥入 Keychain）终止 TLS 并转到网关——那些写死 base URL 只允许配代理的 IDE 插件 / 桌面 App 就能零配置获得路由、故障转移和额度管理。相对 cc-switch（只会改配置文件）是明显差异，但安全权衡大：严格 opt-in、按域白名单、CA 可一键吊销、UI 醒目警示。建议作为后期实验。

---

### 大胆想法：回合级智能降级（实验性）

有了 B 的 Session Key 之后，可以按"回合形态"分类：以 `tool_result` 为主的回合、`max_tokens` 很小的回合（标题/摘要）→ 便宜渠道；含新的用户指令的规划回合 → 高端渠道。作为 opt-in 的"省钱模式"，并用 F 做 A/B 度量质量损失。风险在质量，所以放最后。

---

## 2. 优先级建议

| 顺序 | 特性 | 价值 | 工作量 | 依赖 | 理由 |
|---|---|---|---|---|---|
| 1 | **B 缓存亲和路由** | 极高（省钱可量化） | S–M | 无 | 修正现有 key 轮转带来的隐性损失；E 的前置 |
| 2 | **C 身份一致性** | 高（防封号） | S | 无 | `ProxyURL` 字段已在，接线即可；把浏览器与网关焊在一起 |
| 3 | **A 真伪审计（先被动后主动）** | 极高（叙事核心） | M | 无 | 被动信号一周可上；主动探针复用签到调度器 |
| 4 | **G 限流学习** | 中高 | S–M | 无 | 直接减少无谓故障转移；配合 B |
| 5 | **H MCP** | 中（传播点） | S | 无 | 极低成本的"哇"时刻 |
| 6 | E 失控熔断 / D 对冲 / F 回放实验室 | 高 | M | B / A / A | 在前 5 项数据基础上叠加 |
| 7 | I 透明劫持 | 中 | M | — | 实验分支，安全评审后再定 |

---

## 3. 读代码时顺手发现的缺口（与上述特性直接相关）

1. **跨协议流式未翻译**：`transform.go:652 writeSSE(w, body, protocol)` 的 `protocol` 参数在函数体内未使用，上游 SSE 逐行直通。Anthropic 客户端（Claude Code **总是**流式）→ OpenAI 上游时，客户端会收到 OpenAI 格式的 chunk。非流式路径有 `transformResponse`，流式没有。这是核心场景的正确性问题，建议优先于所有新特性。
2. **能力标志没接线**：`gateway.go:221` 只传了 `Protocol` 和 `Model`；`selector.go:379-387` 的 `ToolCallRequired / VisionRequired / ReasoningRequired` 过滤永远不触发。从请求体的 `tools` / 图片 content / `thinking` 填这三个标志是 20 行的事。
3. **`Channel.ProxyURL` 未使用**（见 C）。
4. **`Retry-After` / `x-ratelimit-*` 未处理**（见 G）。
5. **401 不标记 Key**：`retryableStatus` 包含 401，会换渠道但不会标记该 Key 失效；多 Key 轮转下坏 Key 会被反复选中。需要 Key 级健康（product-roadmap I7 已提到）+ 401 自动禁用。
6. **`RequestRecord` 缺 TTFT / finish_reason / 上游回报的 model / cache tokens**——都很便宜，且是 A、B 的数据前提。
7. `writeSSE` 为提取 usage 把整条流缓存在内存里；做 D（对冲）和 A（完整性检测）时需要换成流式解析器。
8. 文档与代码漂移：ROADMAP-2026 写"策略只有 weighted/RR"，但 `selector.go:431-463` 已有 `fixed / latency / success-rate / quota-aware`。建议以代码为准更新文档，避免 M1 重复造轮子。

---

## 4. 一句话总结

别人卷"更多 provider、更好看的 CRUD"，RelayHub 应该卷 **"在不可信的上游之上，替用户守住钱、账号、时间和质量"**：审计它们（A）、让会话便宜（B）、让账号活着（C）、让延迟有界（D）、不让 Agent 烧钱（E）、能证明谁更好（F）。这条线上目前没有对手。
