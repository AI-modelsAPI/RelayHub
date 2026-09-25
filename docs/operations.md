# 运维配置：出口与通知

RelayHub 的所有非密钥配置来自三处，优先级从高到低：

1. 命令行参数（监听地址等）
2. 环境变量 `RELAYHUB_*`
3. `<data-dir>/config.json`（可选；不存在时全部使用默认值）

密钥永远不放在这里——渠道 Key / Cookie 走加密 secret store。

## 1. 统一出口（Egress）

RelayHub 的三条出站路径——**AI 网关转发**、**签到 / 余额 HTTP 适配器**、**CDP 真实浏览器签到**——使用同一套出口决策：

```
Channel.proxy_url  >  egress_proxy_url（config / 环境变量）  >  进程环境 HTTPS_PROXY 等  >  直连
```

| 设置 | 位置 | 取值 |
|---|---|---|
| 渠道级出口 | 渠道表单 `proxy_url` / `PUT /api/v1/channels/{id}` | `http://…`、`https://…`、`socks5://…`（`socks5h://` 亦可）、`direct`（显式直连，忽略全局与环境） |
| 全局默认出口 | `config.json` 的 `egress_proxy_url` 或 `RELAYHUB_EGRESS_PROXY` | 同上 |

规则：

- 代理 URL 非法（未知 scheme、缺 host）在 API 保存时即被拒绝；运行期若仍遇到非法值，该渠道**拒绝出站**而不是悄悄换成别的出口。
- 浏览器路径使用 Chromium `--proxy-server`。Chromium 不接受 URL 内嵌凭据，因此**带用户名密码的代理会让 CDP 签到拒绝启动**（HTTP 适配器与网关不受影响）。需要认证代理时请在本机放一个无认证的转发端口。
- 查看某渠道实际出口：`GET /api/v1/channels/stats?channel_id=<id>` 的 `egress` 字段（`source` = `channel` / `global` / `environment`，`via` 隐去凭据）；全局默认见 `GET /api/v1/settings` 的 `egress`。

示例 `config.json`：

```json
{
  "egress_proxy_url": "socks5://127.0.0.1:1080",
  "notify": {
    "bark_url": "https://api.day.app/<device_key>",
    "quota_low_usd": 1.0
  }
}
```

## 2. 通知（Notify）

未配置任何 sink 时通知子系统完全关闭、零开销。配置任意一个即启用：

| Sink | `config.json` | 环境变量 | 说明 |
|---|---|---|---|
| Webhook | `notify.webhook_url` | `RELAYHUB_NOTIFY_WEBHOOK_URL` | `POST` JSON：`{source, kind, severity, title, body, channel_id, fields, at}` |
| Bark | `notify.bark_url` | `RELAYHUB_NOTIFY_BARK_URL` | `https://api.day.app/<key>` 或自建地址；错误级事件带 `timeSensitive` |
| Telegram | `notify.telegram_bot_token` + `notify.telegram_chat_id` | `RELAYHUB_NOTIFY_TELEGRAM_TOKEN` + `RELAYHUB_NOTIFY_TELEGRAM_CHAT_ID` | 两者缺一则不启用 |
| 额度阈值 | `notify.quota_low_usd` | `RELAYHUB_NOTIFY_QUOTA_LOW_USD` | 默认 `0.5`（USD） |

事件与触发条件：

| `kind` | 何时 |
|---|---|
| `checkin_failed` | 自动签到重试用尽后仍失败 |
| `checkin_need_manual` | 任何需要人工的降级（渠道设为手动、Turnstile 需人工、无浏览器、浏览器报成功但服务端无法核验） |
| `balance_refresh_failed` | 余额刷新由正常转为失败（每次故障只报一次） |
| `circuit_open` | 渠道连续失败触发熔断 |
| `auth_expired` | 上游返回认证失败 |
| `quota_exhausted` | 观测余额归零，路由已跳过该渠道 |
| `quota_low` | 余额低于阈值 |
| `channel_recovered` | 从熔断 / 额度耗尽 / 认证失效恢复为 healthy |
| `authenticity_suspect` | 真实度探针判定渠道可疑（复述失败、换了模型、工具调用失效），路由已降权；只在由正常转为可疑时发送（见 §5） |
| `test` | `POST /api/v1/notify/test` |

同一 `(kind, channel)` 组合 5 分钟内只发一次；投递异步进行，失败写入日志且不影响主流程。

验证配置：

```bash
TOKEN=$(cat "<data-dir>/management.token")   # 或 config.json 里的 management_token
curl -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8790/api/v1/notify/test
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8790/api/v1/settings | jq .notify
```

## 3. 浏览器签到的选择器覆盖

CDP 签到默认寻找 `#checkin-btn, .checkin-btn, button[data-action="checkin"]`，成功提示 `.checkin-success, #checkin-success, [data-checkin-status="success"]`。站点不同时，把 JSON 写进 Provider 的 `capabilities` 字段即可，无需改代码：

```json
{"checkin_button": "button.sign-in-daily", "checkin_success": ".sign-in-done"}
```

注意：DOM 上的"成功"只是提示。签到调度器随后会用站点自己的记录（签到日历 / 奖励日志）核验；服务端没有记录就记为 `failed`，无法核验记为 `need_manual`，绝不凭页面元素宣布成功。

## 4. 安全相关配置（AUDIT 2026-09-24）

`config.json` 可以存放下面几个凭据类字段，启动时文件会被收紧为 `0600`，数据目录收紧为 `0700`。也可以改用环境变量传入，文件里就不必出现明文。

| 字段 | 环境变量 | 作用 |
|---|---|---|
| `management_auth` | `RELAYHUB_MANAGEMENT_AUTH` | `token`（默认）或 `off`。`token` 模式下，除 `/api/v1/health`、`/healthz` 和配对接口外，所有管理 API（包括读取）都要求 `Authorization: Bearer <token>`。`off` 恢复旧行为：本机任何进程都可以不带凭据读写管理 API，启动日志会打印警告 |
| `management_token` | `RELAYHUB_MANAGEMENT_TOKEN` | 显式指定管理令牌。不设置时，首次启动会随机生成一个，写入 `<data-dir>/management.token`（权限 `0600`），之后重启沿用。与 `management_auth: off` 同时设置会阻止启动 |
| `proxy_username` / `proxy_password` | `RELAYHUB_PROXY_USERNAME` / `RELAYHUB_PROXY_PASSWORD` | 两个都设置时，本地 HTTP 代理要求 Basic 认证，SOCKS5 要求 RFC 1929 认证；只设置其中一个会阻止启动 |
| `master_key_store` | `RELAYHUB_MASTER_KEY_STORE` | `file`（默认，`<data-dir>/master.key`）或 `keychain`（仅 macOS）。选 `keychain` 后主密钥存入登录钥匙串：已有的 `master.key` 会先迁移过去，回读校验通过后才删除文件；如果文件和钥匙串里的值不一致，就拒绝启动。之后备份和同步盘里就不再有明文主密钥（旧备份里仍然有，必要时请轮换渠道 Key）。钥匙串处于锁定状态时启动会失败，不会悄悄生成新密钥 |
| `http_proxy_target_policy` / `socks5_target_policy` | — | 可选 `open`（默认）、`public_private`、`public_only`、`local_only`。无论选哪个，都会拒绝 RelayHub 自身的端口和云元数据地址；CGNAT（100.64/10）按私网处理 |

### 管理 API 认证与控制台配对

管理 API 默认要求令牌（`management_auth: token`）。浏览器通过一次性配对链接拿到令牌，令牌本身不会出现在任何 URL 里：

- 启动日志会打印 `http://127.0.0.1:8790/#pair=<code>`。这个链接只能用一次，10 分钟后过期。
- 运行 `relayhub pair` 会打印一个新链接（需要能读到令牌：`RELAYHUB_MANAGEMENT_TOKEN`、`config.json` 里的 `management_token`，或 `<data-dir>/management.token`；非默认数据目录请加 `-data-dir` 或设置 `RELAYHUB_DATA_DIR`）。
- 配对码放在 URL 的 `#` 片段里，浏览器不会把它发给服务器，也就不会进入访问日志。控制台兑换成功后会把它从地址栏和历史记录中去掉，令牌只保存在该来源的 localStorage 里。
- 没有配对码时，控制台会显示一个登录框，可以粘贴配对链接、配对码，或 `management.token` 里的令牌。
- macOS 桌面应用会自动把令牌交给内嵌网页；菜单里的“在浏览器中打开”会附带一个新的配对链接。
- `relayhub mcp` 按同样的顺序自己查找令牌。CLI 同步写入 Claude Code 的 MCP 条目只包含 `RELAYHUB_DATA_DIR`，不包含令牌。
- 脚本可以直接读 `<data-dir>/management.token`，然后在请求中带上 `Authorization: Bearer $(cat …/management.token)`。
- 审计日志的操作者字段会附带令牌指纹（`token:` 加 SHA-256 前 8 位十六进制），不会记录令牌本身。

### 渠道请求头档案

默认情况下，网关会把客户端 SDK 的身份头（`User-Agent`、`X-Stainless-*`、`anthropic-*`）原样转发，因为不少中转站只接受“看起来像 Claude Code”的请求。如果某个渠道不需要这些头，可以在该渠道的 `custom_headers` 里配置：

```json
{"X-Stainless-*": "", "X-Stainless-Lang": "js", "Anthropic-Beta": ""}
```

- 值为空：删除这个客户端头。
- 名字以 `*` 结尾且值为空：删除所有以该前缀开头的头。
- 先执行删除，再设置非空值，所以可以先整组删掉，再单独保留其中某一项。

`Accept-Encoding`、hop-by-hop 头、`Origin`/`Referer`/`Sec-*`、`OpenAI-Organization`/`OpenAI-Project` 一律不会转发。

### 渠道 Key 与地址绑定

录入 Key 时，它会绑定到渠道当时 `base_url` 的 origin（协议 + 主机 + 端口）。之后如果把 `base_url` 改到另一个 origin，这些 Key 会被锁定：测试渠道时返回 `409 key_origin_mismatch`，网关也不会使用它们。需要在新地址下重新录入 Key。旧版本留下的 Key 在升级后首次启动时，会自动绑定到渠道当时的地址。

### 导出包

- 带密码的导出为 v3 格式：代理密码和敏感请求头只放在加密区，校验值使用随机盐。
- 不带密码的导出只含遮蔽后的值。导入时，已有渠道保留本机原值；新渠道则直接丢弃这些占位值。

### 模型自动同步

- 手动同步和渠道的首次同步保持原有行为：上游列出的模型全部绑定并启用，多个中转站提供同一模型时照常负载均衡。
- 已经上线的渠道在**后台定时同步**中新声明了某个模型，而这个模型已经由其他渠道提供（例如某个中转站突然开始列出 `gpt-4o`）时，绑定会被创建但保持**禁用**，同时发送 `models_held` 通知。确认可信后，在模型页手动启用即可；启用状态在之后的同步中会保留。
- 每次同步最多接受 2000 个模型 ID。ID 最长 200 个字符，不能包含空白或控制字符。运维手工调整过的优先级、权重、启用状态和上游名映射不会被同步覆盖。

## 5. 真实度探针（AUDIT §5 B1）

中转站最常见的问题是「挂羊头卖狗肉」：名义上是 Opus，实际给的是便宜模型、缓存的固定回复，或者把工具调用悄悄丢掉。RelayHub 定期给每个渠道发极小的 canary 请求，给渠道打「真实度」分，并据此调整路由。

**怎么测**：请求走的是和正常流量完全相同的路径（同一套 Key 轮换、渠道出口代理和身份请求头），中转站从传输层看不出这是探针。

| 检查 | 做法 | 不通过时 |
|---|---|---|
| 复述暗号 | 让模型原样复述 4 个随机英文单词 | −60（`canary_failed`） |
| `model` 字段 | 响应里的 `model` 与请求的模型比对家族、档位和版本；只是写法不同（带日期、`-latest`、厂商前缀、Bedrock ID）不算 | −40（`model_mismatch`）；写法不同只记 `model_renamed`，不扣分 |
| 工具调用 | 模型声明支持工具时，再发一次强制工具调用 | −40（`tools_missing`） |
| 分词指纹 | 固定形状的 canary，上报的 prompt token 数应当稳定：与本渠道第一次观测相比（漂移，常见于换了后端或注入了系统提示），以及与服务同一模型的其他渠道相比（离群，需要至少两个渠道意见一致） | 漂移 −25（`fingerprint_drift`），离群 −30（`fingerprint_outlier`） |

- 探针分低于 **70** 即判为可疑：一次复述失败、换了模型或工具失效都够，单独的指纹异常不够。
- 上游没给出可用的 2xx 回答（网络错误、4xx/5xx、HTML 等）时本次探测**不下结论**，既不洗白也不定罪；被截断的回答也不算复述失败。
- 探针请求不计入健康度、用量和被动分，所以某个中转站拒绝探针不会连累正常流量。

**对路由的影响**：可疑渠道在有其他可信渠道可选时被跳过，原因写进路由决策（`GET /api/v1/routes/explain` 可见，形如 `authenticity probe on <模型>: canary_failed (score 40)`）；所有候选都可疑时照常使用，**降权但不断供**。结论 48 小时内有效，过期后不再影响路由（仍会显示）。

**频率与开销**：

| 配置 | `config.json` | 环境变量 | 说明 |
|---|---|---|---|
| 探测间隔 | `verify_probe_interval` | `RELAYHUB_VERIFY_PROBE_INTERVAL` | Go 时长格式，默认 `12h`，最小 `10m`；`off` 或 `0` 关闭定期探测（手动探测仍可用）；非法值启动时报错 |

- 每轮对每个「启用且参与路由、当前可用」的渠道探测一个模型：渠道的 `default_test_model`，否则最近请求最多的模型，否则第一个绑定的模型。渠道之间间隔 2 秒，启动 2 分钟后跑第一轮。
- 每次探测 1–2 个请求，canary 的 `max_tokens` 为 64，工具探测为 256（OpenAI 推理模型改用 `max_completion_tokens` 2048）。
- 分数只保存在内存里，重启后从下一轮探测重新建立（指纹基线也一样）。

**手动探测**：控制台「渠道 → Inspector → 真实度 → 立即探测」，或：

```bash
TOKEN=$(cat "<data-dir>/management.token")
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"channel_id":"<渠道 ID>","model_id":"<可选，逻辑模型 ID 或上游模型名>"}' \
  http://127.0.0.1:8790/api/v1/verify/probe
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8790/api/v1/verify/scores
```

`verify/probe` 返回本次结论（`result`：每项检查的 pass/fail/error/skip 与说明）和渠道综合分（`score`：被动分与各模型最新探针分取较低者，`suspect` 表示正在降权）。渠道没有可探测的已启用模型时返回 404。

## 6. 流式提交窗口与熔断恢复探针（AUDIT §5 B4/B5）

**流式提交窗口**：网关收到上游 2xx 后不会立刻把响应头交给客户端，而是先缓冲到「第一个带输出的事件」（或 10 秒 / 256 KiB 上限）为止。在提交之前出现的错误事件、HTML、空流和非 SSE 内容都算这次尝试失败，可以透明地切到下一个渠道；一个渠道都没有时，客户端拿到的是上游错误对应的真实 HTTP 状态码（429、529、502 等）。提交之后（用户已经看到首字），上下游报到一半失败就无法再切换了：同协议的流原样透传，跨协议时转换成客户端自己的错误格式（Anthropic `event: error` / OpenAI `data: {"error":…}`），并计入用量与真实度（`upstream_error_event`）。

**熔断恢复探针**：熔断的渠道过去在冷却结束后直接重新接收真实流量，等于拿用户请求探活。现在只要恢复探针在运行，冷却结束的渠道会排入半开（half-open）队列，等一次 `max_tokens=1` 的低成本请求确认可用后再回到路由；探活失败则按累计熔断次数指数退避重开。探针走的是和正常流量相同的 Key 轮换、出口代理和身份请求头。

| 配置 | `config.json` | 环境变量 | 说明 |
|---|---|---|---|
| 流式提交窗口 | `stream_commit_window` | `RELAYHUB_STREAM_COMMIT_WINDOW` | Go 时长格式，默认 `10s`，最小 `100ms`；`off` 或 `0` 关闭缓冲、首字节直通（首字更快，但提交后无法再切换渠道）；非法值启动时报错 |
| 恢复探针间隔 | `health_probe_interval` | `RELAYHUB_HEALTH_PROBE_INTERVAL` | Go 时长格式，默认 `30s`，最小 `100ms`；`off` 或 `0` 关闭探针，退回旧行为（冷却结束后由真实流量探活）；非法值启动时报错 |

- 上游对 Anthropic 的 `overloaded` 经常返回 529；现在它和 429/5xx 一样会触发渠道切换，单个渠道时原样返回 529 给客户端。
- 恢复探针每轮只处理冷却期已过的渠道，渠道之间间隔 2 秒，启动 2 分钟后跑第一轮；渠道没有可探测的已启用模型时跳过（不会把它卡在半开状态）。
- 探活请求本身不计入被动真实度分，但结论会直接改写渠道健康：成功即恢复 Healthy，失败则重开熔断并拉长冷却。
