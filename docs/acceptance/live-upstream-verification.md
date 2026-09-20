# 真实中转验证（AxonHub https://ai.nba00.com）+ 熔断恢复缺陷修复

用用户提供的真实中转 key 对已实现功能做端到端验证。全程用客户端虚拟 key（rh_ 本地签发 key + 密封上游 key），不使用后台/master key。

## 验证环境
- 干净数据目录 `/tmp/relayhub-verify.*`，`./bin/relayhub -full-stack`，管理口 127.0.0.1:18790，网关 127.0.0.1:8789。
- 上游：AxonHub，`ah-…`（值仅写入密封 secret store，链路取证时不回显）。

## 实测通过（真实 HTTP 输出）
1. 上游直连 `GET /v1/models` → 200，10 个模型；`POST /v1/chat/completions` gemini-3.8-flash → `PONG`。
2. 管理面建 provider/channel；**加渠道 Key**：响应不含明文、无 secret_ref；list 只回元数据。
3. `POST /api/v1/channels/test` → success, latency 191–538ms, model_count 10（走密封 Key 路径）。
4. `POST /api/v1/channels/sync-models` pattern 过滤 → 命中 3 个模型并重建绑定。
5. 本地签发 `rh_` key；**网关 E2E**：`/v1/chat/completions` → `RELAYHUB_OK`（本地 key → 路由 → 密封上游 key → AxonHub 全链路真实闭环）。
6. `/api/v1/usage` 记录 1 条，input=263/output=5，无 prompt 正文；`/api/v1/channels/stats` health=healthy, enabled_key 1/1, recent 1/1 成功。
7. 跨协议：Anthropic `/v1/messages` → `{"type":"message","stop_reason":...}`；反向 OpenAI 客户端得 `choices[]`。
8. 流式 SSE：`stream:true` 逐块 delta + 末尾 usage，正常。
9. 渠道复制：clone 清空 credential_ref、置 disabled（防误上线）。
10. 模型/渠道批量 archive：archive 后 catalog 默认隐藏，`include_archived=true` 可见。
11. 数据跨重启持久化：channel + 密封 Key 重启后仍在。

## 发现并修复的真实缺陷（先 RED 后 GREEN）

### 熔断器永不恢复（P0，实测复现）
- **现象**：禁用渠道 Key 触发 3 次上游失败 → 渠道熔断 `circuit open`；重新启用 Key 后，网关持续 400 `no available candidates: …circuit open`，轮询 8 次（>60s）永不恢复。
- **根因**：`health.Registry.Available()` 对 `CircuitOpen` 无条件返回 false；且无任何生产代码调用 `AllowProbe`/`RecordSuccess`——`RecordFailure` 在 gateway 里接了线，成功路径却从不回写健康态。熔断一旦触发即永久排除该渠道。
- **RED**：`internal/health/recovery_test.go::TestCircuitOpenRecoversAfterCooldown` —— 复现「cooldown 过后仍 unavailable」，先失败。
- **修复**：
  1. `Available()`：`CircuitOpen` 在 CooldownUntil 过后放行（允许探测），未过则仍拒绝。
  2. `gateway` 成功响应后调用 `Health.RecordSuccess()` 关闭熔断、清零失败计数。
- **GREEN 复验（真实上游）**：用可用模型 claude-opus-4-8，禁 Key→3 次失败→熔断 open→重新启用 Key→**attempt 1 即 200 `RECOVERED`**。修复前 8 次全 500/400，修复后一次恢复。

## 验证/门禁
- `go test ./... -count=1` → **29 包全绿 0 FAIL**。
- 触及包 `-race`（health/gateway/router/api/checkin/repository/storage）全 ok。
- 首轮 `-race ./...` 有 3 个包偶发 FAIL（e2e 公网 `one.one.one.one` 超时、browser CDP 15s 在 race 负载下超时、docker），**逐个隔离重跑全部 PASS**，属并发/网络压力假阳性，非逻辑缺陷。
- `go vet` 空、`gofmt -l` 空、`make build`、`make check-web` 通过。

## 说明与残留
- 上游多数模型（gemini-3.8-flash/gpt-5.6-sol/deepseek-*）当前返回 500 `empty response detected`/402/503——**上游侧问题**，非 RelayHub；claude-opus-4-8 稳定 200，用它完成闭环验证。
- 未 commit/push。
