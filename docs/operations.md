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
| `test` | `POST /api/v1/notify/test` |

同一 `(kind, channel)` 组合 5 分钟内只发一次；投递异步进行，失败写入日志且不影响主流程。

验证配置：

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8790/api/v1/notify/test
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8790/api/v1/settings | jq .notify
```

## 3. 浏览器签到的选择器覆盖

CDP 签到默认寻找 `#checkin-btn, .checkin-btn, button[data-action="checkin"]`，成功提示 `.checkin-success, #checkin-success, [data-checkin-status="success"]`。站点不同时，把 JSON 写进 Provider 的 `capabilities` 字段即可，无需改代码：

```json
{"checkin_button": "button.sign-in-daily", "checkin_success": ".sign-in-done"}
```

注意：DOM 上的"成功"只是提示。签到调度器随后会用站点自己的记录（签到日历 / 奖励日志）核验；服务端没有记录就记为 `failed`，无法核验记为 `need_manual`，绝不凭页面元素宣布成功。
