# M2 开发文档：数据洞察（额度账本 + 成本面板 + 健康时间线）

> 对应 `docs/ROADMAP-2026.md` M2。目标：把已记录的 usage/health 数据变成"看得见的省钱"，给养号农场正反馈。
> 约束：复用现有 `RequestRecord`/`HealthRecord`/价格字段，纯聚合 SQL + 现有 vanilla JS 前端，不加依赖。依赖 M1 的 QuotaState。

## 1. 现状锚点

- `internal/usage/recorder.go` 只存元数据（token/延迟/状态码/渠道/模型），合规不存 prompt——保留此红线。
- `GET /api/v1/usage` 只返回总数/成功率/平均延迟，无维度、无成本。
- `Model.InputPrice/OutputPrice` 已存在（M1 开始消费）。
- `HealthRecord` 已存 `latency_ms` + `checked_at` + `status`，未可视化。

## 2. 任务拆解

### 2.1 ★ 免费额度账本（差异化核心）
- 聚合：每渠道"今日签到获得（来自 M1 QuotaState 变化）/ 已消耗 token / 折算美元"。
- 端点：`GET /api/v1/quota-ledger?day=YYYY-MM-DD` → 每渠道 `{granted, consumed_tokens, saved_usd}`。
- 前端：首页顶部一个大数字卡片"今日省下 $N"（M2 唯一强传播点，优先做）。

### 2.2 成本/用量面板
- 端点扩展：`/api/v1/usage` 增加 `?group_by=channel|model|day` 聚合，返回每维度 `{requests, tokens, cost_usd, success_rate, avg_latency}`。
- 成本 = Σ(input_tokens/1M×InputPrice + output_tokens/1M×OutputPrice)；价格缺失的模型单列"未定价"。
- 前端：渠道/模型两个排行榜 + 7 日趋势（canvas 手绘或纯 DOM 条形，不引图表库）。

### 2.3 渠道健康时间线
- 端点：`GET /api/v1/channels/timeline?channel_id=c&hours=24` → 按时间桶的 `{success_rate, avg_latency}` 序列。
- 前端：渠道展开行内嵌一条 24h 热力条（复用现有 expand row DOM）。

## 3. 验收

1. 新增聚合端点各有 RED→GREEN 测试（含空数据、价格缺失、跨日边界）。
2. 首页真实渲染"今日省下 $N"，数字来自账本端点而非写死。
3. 面板用真实 RequestRecord 数据渲染，截图存档 `docs/acceptance/`。
4. 全量回归链通过（race/vet/fmt/build/check-web）。

## 4. 不做

- 不存 prompt/响应体（沿用 usage 红线）。
- 不引图表库、不做导出报表（导出已有 import-export 模块，够用）。
- 成本是"估算参考"，不做对账级精度。
