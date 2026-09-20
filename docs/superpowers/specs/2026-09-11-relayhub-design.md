# RelayHub 统一 AI 出口与自动签到聚合器设计规格

**状态：已确认设计，待拆分实现计划**  
**版本：0.1.0**  
**日期：2026-09-11**

## 1. 摘要

RelayHub 是一个本地优先的 AI 出口管理器。它聚合五个公益中转站及任意可配置的第三方站点，后台维护用户授权的账号并自动签到领取额度；同时提供透明 HTTP/HTTPS、SOCKS5 代理和 OpenAI/Anthropic 兼容 Gateway，使其他软件只需连接一个本地入口即可访问上游。

RelayHub 以 Provider、Channel、Model、ProviderModel、ModelGroup、Route 六类资源分离站点、具体账号/密钥、统一模型、上游模型映射、模型分组和路由策略。macOS 桌面版与 Docker 版共用 Go Core、数据库模型、Web 控制台、适配器和 API；两端不做实时云同步，通过版本化、可选加密的 `.rhx` 配置包同步。

项目只操作用户拥有或明确授权的账号，不实现批量注册、验证码/Turnstile 绕过、浏览器指纹伪造、账号共享或未授权访问。

## 2. 目标与非目标

### 2.1 目标

- 提供稳定的本地 HTTP/HTTPS 和 SOCKS5 透明代理，转发任意目标请求。
- 提供 OpenAI Chat/Responses 与 Anthropic Messages 兼容入口。
- 内置 AgentRouter、JustDoWork、GoRouter、SeekAI、KKtoken AI 五个签到适配器。
- 允许通过通用配置添加其他 OpenAI/Anthropic/Gemini 兼容站点。
- 允许通过声明式适配器扩展简单的签到、刷新和额度查询流程。
- 管理多个 Provider、Channel 和用户授权账号。
- 自动签到、刷新 Token/Cookie、查询额度并隔离站点故障。
- 将模型和提供商分离，支持模型统一命名、上游名称映射和模型组路由。
- 一键安全同步 Codex、Claude Code、Hermes 配置。
- 提供 macOS Intel/Apple Silicon 和 Docker amd64/arm64 发行物。
- 支持非敏感配置导出与密码保护的完整配置导出，不做实时云同步。

### 2.2 非目标

- 多用户 SaaS、计费和在线账号池交易。
- 公网开放代理或默认远程管理。
- 云端集中保存用户 Cookie、Token 或 API Key。
- 自动注册账号、验证码破解、风控绕过或身份伪造。
- 直接复刻 AxonHub 全部后台功能、成本计费和分布式集群能力。
- 将上游真实密钥散落写入各个 CLI 配置文件。

## 3. 设计原则

1. **透明代理与 AI Gateway 分离**：透明代理处理任意 HTTP/HTTPS/TCP 目标；Gateway 处理标准 AI 协议、模型映射和账号路由；签到层只维护账号状态。
2. **Provider 与 Model 分离**：Provider 表示上游类型，Channel 表示具体出口，Model 表示统一逻辑模型，ProviderModel 表示上游模型映射，ModelGroup 表示业务分组。
3. **故障隔离**：单个站点、适配器或账号异常不能阻止主进程、透明代理或其他出口启动。
4. **默认 fail-closed**：站点协议、身份或配置不明确时停止该操作并给出原因，不猜测接口、不发送伪造请求。
5. **本地优先和最小暴露**：默认仅监听 127.0.0.1；远程访问必须显式开启认证、白名单和限流。
6. **可恢复配置修改**：CLI 配置和数据库变更必须可预览、备份、原子写入、回读验证和恢复。
7. **跨平台共享业务核心**：macOS 壳与 Docker 只处理平台生命周期、密钥存储和打包，不复制业务逻辑。

## 4. 总体架构

```text
┌──────────────────────────────────────────┐
│ Web 控制台 / CLI / macOS 菜单栏壳         │
└──────────────────┬───────────────────────┘
                   │ Local HTTP API
┌──────────────────▼───────────────────────┐
│                 Go Core                  │
│ Provider/Channel/Model/Route Registry    │
│ Router · OpenAI/Anthropic Gateway        │
│ HTTP CONNECT · SOCKS5 Proxy              │
│ Check-in Scheduler · Adapter Runtime     │
│ Secrets · SQLite · Usage · Diagnostics   │
│ CLI Config Sync · Import/Export           │
└───────────────┬───────────────┬──────────┘
                │               │
       五个内置/扩展站点       任意透明代理目标
```

### 4.1 默认端口

| 服务 | 默认地址 | 用途 |
|---|---|---|
| HTTP 代理 | `127.0.0.1:8787` | HTTP 转发、HTTPS CONNECT |
| SOCKS5 | `127.0.0.1:8788` | TCP 代理 |
| AI Gateway | `127.0.0.1:8789` | OpenAI/Anthropic 兼容 API |
| 管理 API/Web UI | `127.0.0.1:8790` | 配置、状态、日志和控制 |

端口可配置；启动时检查冲突并明确报告，不静默改写已有配置。

### 4.2 数据流

AI 请求处理顺序：

```text
客户端
  → 协议识别与本地认证
  → 解析 model 或 model group
  → ModelGroup 选择 Model（如请求的是组）
  → 查找启用的 ProviderModel 绑定
  → 过滤健康 Channel、协议能力和额度状态
  → 按 Route 策略选择 Channel
  → 映射 upstream_model_name
  → 请求转换并发送上游
  → 流式/非流式响应转换
  → 记录脱敏元数据
```

透明代理不进入模型路由链，只执行目标地址解析、连接、转发、超时和审计元数据记录。

## 5. 核心资源模型

### 5.1 Provider：提供商

Provider 表示上游服务类型或站点适配器，不代表单个密钥或账号。

```text
Provider
├── id
├── name
├── adapter_type: builtin | generic | declarative | script
├── protocol: openai-chat | openai-responses | anthropic-messages | gemini | transparent
├── base_url_template
├── capabilities
├── checkin_enabled
├── adapter_version
└── enabled
```

Provider 负责默认协议、适配器、站点级能力和签到规则。Provider 不直接保存凭据，不直接承载某一个账号的余额。

### 5.2 Channel：渠道/具体出口

Channel 是实际可用的上游连接，可以是一个账号、API Key、Cookie 会话或一个独立网关出口。

```text
Channel
├── id
├── provider_id
├── name
├── base_url
├── credential_ref
├── account_ref
├── priority
├── weight
├── routing_tags
├── quota_state
├── health_state
├── rate_limit_state
├── checkin_enabled
├── routing_enabled
└── enabled
```

同一 Provider 可以有多个 Channel，例如 `AgentRouter-账号A`、`AgentRouter-账号B`。签到和路由是两个独立开关：一个 Channel 可以参与签到但不参与模型路由，也可以只作为 API 出口。

### 5.3 Model：统一逻辑模型

Model 不绑定 Provider，用于客户端可见名称、能力、统计和模型组。

```text
Model
├── id
├── display_name
├── aliases
├── family
├── protocol_requirements
├── capabilities
├── context_window
├── max_output_tokens
├── reasoning_support
├── vision_support
├── tool_call_support
└── enabled
```

### 5.4 ProviderModel：上游模型绑定

ProviderModel 将统一 Model 映射到一个 Provider 或具体 Channel 的真实模型名称。

```text
ProviderModel
├── id
├── provider_id
├── channel_id (nullable)
├── model_id
├── upstream_model_name
├── protocol
├── priority
├── weight
├── request_transform
├── response_transform
└── enabled
```

示例：统一模型 `claude-sonnet` 可以映射为：

```text
AgentRouter-账号A → claude-sonnet-4-20250514
OpenRouter-主Key  → anthropic/claude-sonnet-4
自定义网关         → claude-sonnet
```

### 5.5 ModelGroup：模型组

ModelGroup 是用户分组和路由的主要入口。

```text
ModelGroup
├── id
├── name
├── description
├── strategy
├── fallback_group_id
└── enabled
```

通过 `model_group_members` 关联 Model，并为成员定义组内 priority/weight。预置分组可包含 `coding`、`reasoning`、`fast`、`vision`；用户可新增任意分组。

客户端可以请求具体模型（`claude-sonnet`），也可以请求模型组（`coding`）。请求模型组时先选逻辑 Model，再选择 ProviderModel 和 Channel。

### 5.6 Route：路由规则

Route 决定某种协议、模型或模型组请求使用哪个策略。

```text
Route
├── id
├── name
├── protocol
├── model_pattern
├── group_id
├── channel_filter
├── strategy
├── retry_policy
├── fallback_policy
└── enabled
```

匹配顺序必须确定且可展示：精确模型 > 模型组 > 通配模型 > 默认规则。冲突时拒绝保存或要求用户明确优先级，不依赖数据库返回顺序。

## 6. 路由、重试与健康状态

### 6.1 Channel 过滤

依次排除：

1. 禁用 Channel、认证失效、需要人工验证的 Channel。
2. 熔断中的 Channel。
3. 明确额度不足或当前模型不可用的 Channel。
4. 不支持目标协议、模型能力或请求类型的 Channel。
5. 超出并发限制或冷却窗口的 Channel。

### 6.2 选择策略

支持：

- 固定 Channel
- 优先级
- 加权轮询
- 延迟优先
- 成功率优先
- 额度优先
- 成本优先（仅在有可靠成本数据时）
- 随机

同一优先级按显式 weight 选择。策略、候选列表、被排除原因和最终 Channel 必须进入脱敏诊断日志。

### 6.3 错误处理

统一错误分类：`client_error`、`authentication_error`、`authorization_error`、`rate_limited`、`quota_exhausted`、`provider_unavailable`、`provider_protocol`、`timeout`、`network_error`、`verification_required`、`configuration_error`、`internal_error`。

| 错误 | 有限重试 | 切换 Channel | 熔断 |
|---|---:|---:|---:|
| 参数错误/模型不存在 | 否 | 否 | 否 |
| 401/Token 失效 | 可刷新一次 | 是 | 是 |
| 403 权限不足 | 否 | 是 | 可选 |
| 429 | 遵循 Retry-After | 是 | 短时 |
| 500/502/503 | 是 | 是 | 连续失败后 |
| 超时/网络错误 | 是 | 是 | 连续失败后 |
| 人工验证 | 否 | 否 | 是 |
| 流式已开始后的中断 | 不重放 | 否 | 记录 |

流式响应发送首个有效事件后，禁止切换 Channel 或重放请求，避免响应拼接和副作用重复。

## 7. 站点适配与签到

### 7.1 内置 Provider

第一版内置：

- AgentRouter
- JustDoWork
- GoRouter
- SeekAI
- KKtoken AI

每个适配器独立实现并版本化，统一接口：

```go
type ProviderAdapter interface {
    Validate(ctx context.Context, channel Channel) error
    Refresh(ctx context.Context, channel Channel) error
    CheckIn(ctx context.Context, channel Channel) (CheckInResult, error)
    Balance(ctx context.Context, channel Channel) (BalanceResult, error)
    Models(ctx context.Context, channel Channel) ([]ModelInfo, error)
    Health(ctx context.Context, channel Channel) (HealthResult, error)
}
```

适配器不得让主服务依赖某个站点成功加载；加载失败时只标记该 Provider/Channel 异常。

### 7.2 通用 Provider

用户可直接创建 `generic` Provider/Channel：填写名称、Base URL、协议、凭据引用、默认模型和路由参数。适合 OpenAI Chat/Responses、Anthropic Messages、Gemini 兼容站点。

通用 Provider 不猜测签到接口，也不自动登录。需要签到时必须使用声明式或内置适配器。

### 7.3 声明式适配器

声明式适配器仅允许预定义 HTTP 方法、路径、Header 模板、字段提取和成功条件。例如签到和额度流程通过 JSON/YAML 描述。它不允许任意系统命令、动态代码、验证码绕过或访问主数据库。

### 7.4 脚本适配器

脚本适配器列为后续能力，不进入最小版本。实现时必须使用独立子进程、工作目录、环境变量白名单、超时/资源限制和 JSON 输入输出；脚本不得直接读写主数据库或输出未脱敏秘密。

### 7.5 调度规则

- 每个 Channel 独立启用/禁用。
- 默认每日一次，执行时间可配置并带 0 至 10 分钟随机延迟。
- 网络错误最多有限重试，429 遵循 Retry-After，使用指数退避。
- 同一 Channel 的签到/刷新任务互斥。
- 重启后恢复任务状态，不重复执行已确认成功的签到。
- 认证失效不隐式触发新的 OAuth；需要用户授权时暂停并通知。
- 人工验证必须由用户在正常 WebView/浏览器中完成，不自动绕过。
- 签到失败不得影响透明代理和其他 Provider。

## 8. 本地代理与 AI Gateway

### 8.1 透明代理

HTTP 代理必须支持普通 HTTP 转发和 HTTPS CONNECT；SOCKS5 支持 TCP、IPv4/IPv6、可选用户名密码、连接超时和空闲连接回收。请求方法、查询、Header、请求体、响应状态、Header 和流式响应默认透明转发。

透明代理不记录完整 URL 中的敏感查询参数，不记录请求体，不参与 AI 账号路由。

### 8.2 OpenAI Gateway

至少支持：

```text
GET  /v1/models
POST /v1/chat/completions
POST /v1/responses
POST /v1/embeddings
```

支持非流式、SSE、工具调用、图片输入（上游能力允许时）、请求取消、统一错误格式和模型/Channel 映射。

### 8.3 Anthropic Gateway

至少支持：

```text
GET  /v1/models
POST /v1/messages
```

支持 `messages`、`system`、`tools`、`tool_choice`、图片内容、SSE、usage 和错误格式。Claude Code 使用不带 `/v1` 的 Anthropic Base URL；OpenAI SDK/Codex 使用带 `/v1` 的 OpenAI Base URL。

### 8.4 本地认证

Gateway 使用 RelayHub 本地 API Key。默认不把上游真实 API Key 写入 CLI 配置。撤销本地 Key 必须能立即阻断对应客户端，但不删除上游凭据。

## 9. CLI 配置同步

### 9.1 通用流程

```text
检测 CLI → 定位实际路径 → 解析 → 差异预览 → 创建备份
→ 结构化合并 → 原子写入 → 回读验证 → 最小健康检查
```

解析失败、符号链接风险、权限不足或无法安全合并时停止写入并保留原文件。所有同步操作必须幂等，不删除用户未选择的字段。

### 9.2 Codex

目标为 `$CODEX_HOME/config.toml`，未设置时使用 `~/.codex/config.toml`。同步器增加或更新 `model_providers.relayhub`、`base_url`、`wire_api`、模型和 provider 选择，保留 sandbox、approval、MCP、项目设置和其他 Provider。字段必须按检测到的 Codex 版本校验。

### 9.3 Claude Code

目标为 `$CLAUDE_CONFIG_DIR/settings.json`，未设置时使用 `~/.claude/settings.json`。同步器结构化合并 `ANTHROPIC_BASE_URL`、本地认证方式和 Opus/Sonnet/Haiku 模型映射，保留 hooks、permissions、MCP 和其他字段。Anthropic Base URL 默认指向 `http://127.0.0.1:8789`。

### 9.4 Hermes

必须按当前 `$HERMES_HOME` 或 profile 解析：

```text
$HERMES_HOME/config.yaml
$HERMES_HOME/.env
```

优先使用 Hermes 官方 CLI 配置入口；未覆盖的字段才使用安全 YAML 合并。Provider、模型和 Base URL 写入 `config.yaml`，本地 API Key 写入 `.env`。不得把普通设置写进 `.env`，不得把秘密写入 `config.yaml`，不得覆盖 skills、memory、sessions、gateway 和 display 配置。默认 Provider 或 delegation Provider 变化必须在预览中明确显示。

### 9.5 Profile

支持“合并当前配置”和“生成独立 RelayHub profile”。默认推荐独立 profile，并提供设为默认、切回原配置和恢复备份操作。

## 10. Web 控制台与 API

菜单：概览、提供商、渠道/账号、模型、模型组、路由规则、自动签到、请求与用量、CLI 同步、配置导入导出、系统设置。

概览至少展示服务状态、代理地址、Gateway 地址、Provider/Channel 健康、今日签到结果、可用额度、请求数、成功率和最近错误。

管理 API 必须具备：

- 版本化路径和稳定错误结构。
- 写操作幂等键或明确冲突检测。
- 敏感字段只返回掩码和引用。
- 管理操作审计。
- 默认本机认证；远程开启时必须强制管理 Token。

## 11. 存储与秘密

### 11.1 数据表

最小表集合：

```text
providers
channels
credentials
models
provider_models
model_groups
model_group_members
routes
checkin_records
health_records
request_records
cli_sync_records
config_backups
schema_migrations
```

`request_records` 只保存请求 ID、协议、统一模型、Provider、Channel、状态码、延迟、Token 用量和错误分类，不保存完整 Prompt、请求体或响应体。

### 11.2 凭据

Cookie、Access/Refresh Token、API Key、密码和 TOTP 只保存加密值。macOS 优先使用 Keychain 保护主密钥；Docker 使用用户提供的挂载密钥文件或初始化秘密。密钥保护不可用时拒绝明文降级。

### 11.3 配置包

文件格式为 `relayhub-export.rhx`，包含版本化 manifest、非敏感配置、校验和；完整导出额外包含认证加密的秘密区。完整导出使用 Argon2id 派生密钥和 AES-256-GCM 认证加密，不在包内保存密码。

导入流程必须校验格式、版本、完整性和冲突，展示预览后原子导入；默认不覆盖已有 Provider、Channel 或凭据。支持 schema 迁移和失败回滚。

## 12. macOS 与 Docker

### 12.1 共享内容

Go Core、SQLite schema、适配器、Router、Gateway、代理、Web UI、CLI 同步器、日志、导入导出和测试完全共享。

### 12.2 macOS 独有

`.app`、菜单栏常驻、开机启动、原生通知、Keychain、Intel/Apple Silicon DMG、自动更新和本机 CLI 路径检测。桌面壳不复制业务逻辑。

### 12.3 Docker 独有

单容器、`/data` 持久化卷、stdout 日志、非 root、健康检查、数据库迁移、Docker Compose 和 amd64/arm64 镜像。镜像不内置账号或凭据；管理端口默认不暴露公网。

数据目录：

```text
/data/relayhub.db
/data/backups/
/data/exports/
/data/logs/
/data/runtime/
```

### 12.4 同步边界

macOS 与 Docker 不做实时状态或云端同步，只通过 `.rhx` 配置包同步。多实例同时运行时不假设存在共享签到锁，导入和启动界面必须提示避免同一 Channel 被重复签到。两端使用相同版本号、数据库 schema、配置包 schema 和 Web API 版本。

## 13. 安全与合规

- 默认所有服务绑定 `127.0.0.1`。
- 开启局域网/远程访问时强制认证、IP 白名单、速率限制和审计。
- 日志禁止完整 Key、Cookie、Authorization、OAuth code/state、密码、TOTP、Prompt 和敏感错误体。
- 站点协议变化、身份不明或配置不完整时 fail-closed。
- 仅操作用户本人或明确授权的账号。
- 遇验证码、人机验证或设备验证时暂停并要求用户正常完成。
- 不自动重试可能产生副作用的签到/写操作，除非适配器明确声明幂等。

## 14. 测试与验收

### 14.1 单元测试

覆盖 Provider/Channel/Model/ModelGroup 路由、优先级/权重、熔断恢复、Retry-After、错误分类、调度互斥、加解密、配置包校验、TOML/JSON/YAML 结构化合并和备份恢复。

### 14.2 协议测试

覆盖 OpenAI Chat/Responses 非流式和 SSE、Anthropic Messages 非流式和 SSE、工具调用、图片、取消、错误转换、429/5xx 故障转移和首字节后禁止切换。

### 14.3 代理测试

覆盖 HTTP→HTTP、HTTP→HTTPS CONNECT、SOCKS5→HTTP/HTTPS、IPv4/IPv6、大请求体、长连接、流式、超时、客户端取消和认证。

### 14.4 适配器测试

每个内置 Provider 覆盖身份检测、刷新、签到成功/已签到、额度查询、401/403/429/5xx、字段缺失、人工验证和接口版本变化。真实凭据不得进入仓库或 CI。

### 14.5 CLI 同步测试

覆盖空配置、已有复杂配置、多 Provider、profile、路径环境变量、格式错误拒绝写入、备份恢复、重复执行幂等、字段保留和写入后回读验证。

### 14.6 发布测试

macOS 验证 Intel/Apple Silicon 构建、`.app` 启停、Keychain、通知和配置同步；Docker 验证 amd64/arm64、空目录启动、SQLite 持久化、迁移、非 root、健康检查、优雅停止和重启恢复。

## 15. 分阶段交付

### Phase 0：共享 Core 基础

Go 项目结构、SQLite schema、配置/秘密、日志、HTTP API、Web 骨架、Docker 启动和 macOS Core 启动。验收：两端可启动同一 Core、健康状态可见、数据库可迁移备份、无默认公网监听。

### Phase 1：透明代理

HTTP、CONNECT、SOCKS5、超时、取消、认证、日志和健康检查。验收：HTTP/HTTPS 通过 HTTP 代理和 SOCKS5 成功访问测试上游。

### Phase 2：统一 Gateway 与资源模型

Provider、Channel、Model、ProviderModel、ModelGroup、Route、OpenAI/Anthropic Gateway、SSE、工具调用、路由/熔断/用量。验收：SDK、Codex 和 Claude Code 可通过本地入口请求并完成失败切换。

### Phase 3：通用 Provider 与 CLI 同步

通用站点、模型映射、Codex/Claude/Hermes 同步、预览、备份、恢复和健康检查。验收：新增兼容站点不改 Core；三种 CLI 配置安全合并且原字段不丢失。

### Phase 4：五站点签到

五个内置适配器、多 Channel、刷新、签到、额度、健康、任务恢复和人工验证提示。验收：各站点可独立启停；一个 Provider 故障不影响其他出口；额度不足自动退出路由。

### Phase 5：跨平台发布

macOS 菜单栏壳、两种 DMG、Docker 多架构、Compose、`.rhx`、迁移、自动更新和发布流水线。验收：两端同版本共享功能，配置包可互导，迁移失败可恢复。

## 16. 可观测性与诊断

所有请求和后台任务使用 request ID/task ID。诊断记录包含时间、组件、Provider/Channel 脱敏标识、模型、状态码、延迟、错误分类、重试和路由原因。提供健康检查、适配器状态、任务历史、日志级别和脱敏诊断包导出；诊断包默认排除所有秘密、完整请求体和用户内容。

## 17. 设计验收结论

本规格满足已确认范围：

- 五个公益站点自动签到领取额度。
- 任意第三方兼容站点可添加。
- 所有非五站点请求可经本地透明代理代发。
- OpenAI/Anthropic 统一 Gateway 支持 CLI 和 SDK。
- Provider、Channel、Model、ProviderModel、ModelGroup、Route 分离，便于分组和路由。
- Codex、Claude Code、Hermes 配置可预览、备份、结构化同步和验证。
- macOS 与 Docker 共用业务 Core，不做实时云同步。
- 凭据本地加密、默认本机监听、站点故障隔离、敏感日志脱敏。
- 明确了测试、发布和分阶段交付边界。
