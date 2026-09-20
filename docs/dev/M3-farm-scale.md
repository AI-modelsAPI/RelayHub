# M3 开发文档：农场纵深（多节点 peering + 签到 DSL + CLI 洞察）

> 对应 `docs/ROADMAP-2026.md` M3。目标：把养号农场从单机推向规模化，并降低新站点接入门槛。
> 前置：M1 飞轮已闭环（否则多机放大的是断开的链路）。高风险，逐项目独立评审。

## 1. ★ 多节点农场（Farm Peering）— 抗封 + 扩量

- 现状：`internal/api/server.go` 已有 `peerAllowed` 雏形；代理模块（http/socks5）已支持出站代理。
- 设计：主节点持有渠道/凭据（secrets 不下发明文，只下发任务）；从节点（不同 IP）认领签到任务执行并回传结果。
- 任务：
  1. 节点注册/心跳端点 `POST /api/v1/farm/heartbeat`（节点 ID + 能力 + 版本）。
  2. 任务分发 `GET /api/v1/farm/tasks?node_id=`（只回非密元数据 + 一次性凭据句柄）。
  3. 结果回传 `POST /api/v1/farm/results` → 写入 CheckinRecord + QuotaState（复用 M1 路径）。
- 安全红线：凭据明文永不落从节点磁盘；回传通道走现有 authorize + 节点令牌。
- RED→GREEN：主从各起一实例，跑通"分发→从执行签到→回传→额度入账"。

## 2. 签到策略 DSL（降低新站点门槛）

- 现状：adapter 是 Go 插件（agentrouter/gorouter/justdowork/kktoken/seekai 5 个），新站点要写 Go。
- 设计：声明式 YAML DSL 描述签到流程（导航 URL / 等待选择器 / 点击 / 提取 Reward 正则 / turnstile 兜底标记），由 `internal/browser/cdp` 执行器解释执行。
- 任务：DSL schema + 校验器 + 一个解释型 adapter（把 DSL 编译为 CDP 步骤）+ 用 DSL 复刻一个现有 adapter 并通过同一套签到测试。
- 价值：用户贡献新站点不改 Go、不重编译。这是农场生态的关键。
- RED→GREEN：同一站点，Go adapter 与 DSL 版本签到结果一致。

## 3. CLI 用量洞察

- 现状：`internal/clisync` 已能接管 Claude/Codex/Hermes 配置指向本网关。
- 设计：在网关侧按"来源 CLI"（经自定义头/UA 标记）聚合用量，回答"Claude Code 本月烧了哪个模型多少 token/钱"。
- 任务：clisync 注入来源标识头 → RequestRecord 增 source 维度 → M2 面板加 CLI 视图。
- RED→GREEN：带标识头的请求被正确归类到来源 CLI。

## 2'. 顺序与门禁

1. 先做 **签到 DSL**（单机内可闭环、风险低、立刻降低接入成本）。
2. 再做 **CLI 洞察**（纯增量，复用 M2）。
3. 最后做 **多节点 peering**（分布式 + 凭据安全，必须 M1 稳了再上，且强制独立只读审查）。

## 3'. 验收与不做

- 验收：每个子项独立 RED→GREEN + 真实多进程/多实例跑通证据。
- 不做：不做节点间的模型路由转发（peering 只管签到任务分发，不变成分布式网关）；不做凭据明文出主节点；不做第三方 DSL 沙箱逃逸面（DSL 只能是受限动作集）。
