# RelayHub 产品规划书

> 规划文档，非验收报告。评审日期 2026-09-19。仅规划，不含代码改动。
> 现状证据来自两轮独立只读代码审查（前端使用闭环 + 数据/架构）与竞品官方资料检索。

## 1. 定位

RelayHub 的原点（`IDEA.md`）：**公益中转自动签到，并统一走本地代理出口。** 由此收敛出一句话定位：

> 面向个人开发者的**本地优先 AI 网关 + 公益中转保活器**：把多个中转站/公益站的每日签到、额度保活、多 Key 轮询、协议转换、故障熔断收拢到一个本机进程，客户端只认 `127.0.0.1` 与一次性虚拟 Key。

与竞品的边界（检索所得，见 Sources）：
- LiteLLM 是服务端网关，强在虚拟 Key/预算/团队治理与路由组，但需 Postgres+Redis、面向团队部署。[2][3]
- Bifrost 强在按 Key 轮换、虚拟 Key 预算分层、provider 级预算。[3]
- AxonHub 强在 100+ provider、端到端 tracing、RBAC、<100ms 故障切换，面向企业。[4]
- New API 强在渠道多 Key 轮询、优先级/权重、参数覆盖、额度倍率计费，是"中转站服务端"。[5]
- CC Switch 是桌面端 provider 切换器 + 本地路由/故障转移，面向 Claude Code/Codex/Gemini 客户端配置分发，无签到能力。[1]

**RelayHub 的差异位**：唯一把「公益中转**自动签到保活**」与「本地网关**协议转换/熔断/多 Key 轮询**」合到一个**零外部依赖单进程**（Go + SQLite，无 Node/Postgres/Redis）里的本地优先工具。这是竞品都不覆盖的交集，不是"全球唯一网关"，而是"签到保活 × 本地分发"这一具体场景的差异化。

## 2. 现有功能改进（来自前端使用闭环审查，逐条有 path:line）

按用户可感知严重度排序。均为规划项，实施前先写 RED 行为测试。

| # | 问题 | 位置 | 用户后果 | 目标验收 |
|---|---|---|---|---|
| I1 | 新建渠道无 Key 时无必填校验/引导 | `web/app.js:776-790` | 建出静默失效空渠道，调用必错且难排查 | 不填 Key 保存即时高亮阻止，或明确标注免认证 |
| I2 | 新建/编辑 Key 交互割裂（新建多行框 vs 编辑伪密码禁用+底部子表） | `web/app.js:428-433,513-520,792-798` | 用户以为点👁可改现有 Key，实际改不动/困惑 | 编辑态主 Key 区明确"由下方多 Key 列表接管"，或统一为可增删改列表 |
| I3 | 模型映射保存每次全量 POST、无 diff 清理 | `web/app.js:803-806` | 改映射残留孤儿绑定，中途失败产生脏路由 | 保存走先清后写/全量替换，编辑后旧映射彻底注销 |
| I4 | 全局错误统一 Toast、数秒消失、吞状态码/响应体 | `web/app.js:134-142,811,821` | 401/502/超时无法查上下文、无法复制、无重试 | 关键操作失败弹可复制详情（状态码+body）+重试 |
| I5 | 网关本地密钥仅单次明文展示，无强确认 | `web/app.js:1166-1185` | 刷新/切 Tab 即永久丢失，需重新生成并重配所有客户端 | 生成后专有模态，"已复制确认"才可关，附客户端配置样例 |
| I6 | 导入导出仅大文本框粘贴，无文件上传/下载 | `web/app.js:1090-1138` | 大配置复制易截断/卡顿，跨设备备份不可靠 | 加"导出为 .json 文件"与"选择文件导入"，校验哈希 |
| I7 | 多 Key 管理无权重/优先级/独立健康反馈 | `web/app.js:584-624` | 看不出哪个 Key 失效/限流，无法调度 | 每 Key 展示独立计数/健康点，可设权重 |
| I8 | 归档态在筛选器/批量后可能与列表不一致 | `web/app.js:274-279,706-714` | 筛选定位不到归档渠道、批量后徽章与实际漂移 | 筛选"已归档"精准展示，批量后徽章即时同步 |
| I9 | macOS 端口冲突仅告警退出，无换端口/指引 | `desktop/macos/RelayHubApp/main.m:291-306` | 普通用户不知如何改端口/查占用 → 开箱失败 | 冲突弹窗给"用备用端口/查看占用/打开终端"选项 |
| I10 | 菜单栏快捷操作不足 | `desktop/macos/RelayHubApp/main.m:549-563` | 缺状态指示、复制 Base URL、一键暂停/签到 | 菜单栏显示状态+端口+复制 Base URL+快速签到 |

## 3. 差异化新功能方向（贴合当前形态，非"全球唯一"）

1. **中转站保活沙箱 (Check-in & Health Keeper)** — 贴合 IDEA 原点：多级签到（纯 API → 浏览器 Session → 人工兜底）、额度剩余/过期主动抓取与预测、额度低于阈值自动降权隔离，实现"永不断粮"。这是竞品都没有的（LiteLLM/Bifrost/AxonHub/New API 都不做公益站签到保活[2][3][4][5]）。
2. **面向编程 Agent 的语义重试路由** — 识别 tool_calls/streaming 阶段断连截断，429/截断时无缝降级到同能力备用渠道继续输出。区别于 CC Switch 的"切配置"[1]，RelayHub 在请求内做保活续传。
3. **本地零信任凭据隔离** — 上游 Key 入系统 Keychain/Secure Enclave，客户端只见 localhost + 一次性虚拟 Token，出站按规则脱敏。把"本地优先"做成可验证的安全卖点。

## 4. 优先级与阶段门禁

先修数据完整性/安全（P0），再补可靠性（P1），再做差异化（P2）。每阶段先写 RED 测试，全绿 + 独立只读审查通过才算完成；涉及凭据/状态控制的改动必须独立审查、fail-closed。详见配套开发文档 `docs/superpowers/specs/2026-09-19-persistence-and-hardening.md`。

- **P0 数据与安全**：导入导出补全关联实体 + 密钥原子导入（GAP-02）；渠道删除清理孤儿密钥（GAP-01）；建/删密钥逆向补偿（GAP-09）；前端 I1/I3（无 Key 校验、映射 diff）。
- **P1 可靠性**：调度器重启按 checkin_records 水合、杜绝重启重签风暴（GAP-03）；后台并发有界 Worker（GAP-06）；request_records 异步批写解耦单连接（GAP-08）；前端 I4/I5/I6（错误详情、密钥确认、文件导入导出）。
- **P2 高可用与差异化**：控制面事务后 checkpoint 屏障（GAP-04）；macOS Keychain 主密钥（GAP-05）；健康状态持久化打通熔断决策（GAP-07）；VACUUM INTO 一致性备份 + 还原（GAP-10）；差异化方向 1/2/3 择一立项。

## 5. 交付原则

- 不因拆分工作取消 `.superpowers/axonhub-replication-plan.md` 原需求；Tier 2 真实运行时下载校验和仍开放。
- 每批产出 `docs/acceptance/<topic>.md`，分实测/推断，结尾诚实未完成清单。
- UI 交付必须真实浏览器操作 + 实时页面核对，不以 DOM 存在冒充通过。
- 未经用户同意不改阶段顺序、不 commit/push。

## Sources

[1] https://github.com/farion1231/cc-switch — cc-switch
[2] https://docs.litellm.ai/docs/router_architecture — litellm
[3] https://docs.getbifrost.ai/features/retries-and-fallbacks — bifrost
[4] https://github.com/looplj/axonhub/blob/unstable/README.en-US.md — axonhub
[5] https://docs.newapi.pro/en/docs/guide/feature-guide/admin/channel — new-api
