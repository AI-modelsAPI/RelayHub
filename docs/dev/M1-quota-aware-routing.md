# M1 开发文档：飞轮闭环（额度感知路由 + 签到自愈 + 成本路由）

> 对应 `docs/ROADMAP-2026.md` M1。目标：把 checkin（养号）与 router（路由）两个独有模块焊成飞轮。
> 约束：先 RED 行为测试 → 实现 → 真实验证；不新增运行时依赖；涉及凭据/状态的改动需独立只读审查。

## 1. 范围与现状锚点

| 涉及文件 | 现状（代码实证） |
|---|---|
| `internal/checkin/scheduler.go` | 签到成功写 `CheckinRecord.Reward`（字符串），但不解析、不回写渠道额度 |
| `internal/domain/entities.go` | `Channel.QuotaState` 是自由字符串，无结构化"剩余额度/今日可用"语义 |
| `internal/router/selector.go` | `selectCandidate` 只看 strategy/weight/priority/health；`Request` 无成本/额度因子 |
| `internal/domain/entities.go` | `Model.InputPrice/OutputPrice` 字段存在但路由、用量均未消费 |
| `internal/checkin/scheduler.go` | 失败仅记 record，无连续失败计数、无降权、无告警钩子 |

## 2. 任务拆解（每个先 RED 后 GREEN）

### 1.1 结构化额度状态
- **RED**：`internal/domain` 测试——`QuotaState` 解析为 `{free_quota_remaining float64, granted_at, source}` JSON；非法值安全降级为"未知"。
- 实现：`domain.Channel` 增加方法 `QuotaInfo()`（解析 QuotaState JSON，容忍旧的自由字符串）。
- 兼容：旧的非 JSON 字符串一律视为"无免费额度数据"，不报错。

### 1.2 签到成功回写额度
- **RED**：`internal/checkin` 测试——adapter 返回 Reward 含数值（如 `"quota+500"` / `"$0.5"`）时，scheduler 成功路径应更新 `Channel.QuotaState`。
- 实现：scheduler 成功分支解析 Reward → 累加/刷新 QuotaState（带 granted_at 时间戳）→ 经 repo 持久化。Reward 解析失败不阻断签到，仅记 audit。
- 安全：只写 QuotaState，不触碰凭据。

### 1.3 额度感知路由
- **RED**：`internal/router` 测试——同模型两渠道，A 有剩余免费额度、B 无，在 `strategy=quota_first` 下必须选 A；额度为 0 后回退原策略。
- 实现：`Request` 增 `PreferFreeQuota bool`；`selectCandidate` 在 `quota_first`/`cost_first` 策略下把"有剩余免费额度"作为第一排序键，其后退回 weight/priority/health。

### 1.4 成本感知路由
- **RED**：同模型多渠道，`strategy=cost_first` 时优先 `InputPrice+OutputPrice` 折算最低者；价格未设置（0）的渠道排最后而非最前（避免"未定价=免费"误判）。
- 实现：候选排序加入单价键；价格 0 视为"未知"置底。

### 1.5 签到自愈
- **RED**：`internal/checkin` 测试——同一渠道连续失败 N 次（可配，默认 3）→ 自动 `RoutingEnabled=false` 或 weight 降级 + 写一条告警 audit + 触发一次浏览器人工兜底通知。
- 实现：scheduler 维护每渠道连续失败计数；成功即清零；达阈值执行降权并落 `CheckinRecord`/audit。提供 `ManualReviewRequired` 标记，前端渠道行显示"需人工"。

## 3. 验收（真实运行证据，非静态勾选）

1. `go test -race ./internal/domain/ ./internal/checkin/ ./internal/router/` 全绿，含上述 RED 用例。
2. 起真实 relayhub，造一个"有额度渠道 + 无额度渠道"，发同模型请求，日志/用量记录证明打到了有额度渠道。
3. 模拟连续签到失败 3 次，确认渠道被自动降权且出现"需人工"标记。
4. `go vet ./...`、`gofmt -l cmd internal tests`、`make build` 干净。

## 4. 明确不做

- 不做真实计费/扣费（额度仅是路由信号，非账本结算——账本在 M2）。
- 不改 adapter 插件接口签名（只在 scheduler 成功分支消费 Reward）。
- 不引入第三方规则引擎。

## 5. 风险

- Reward 格式各站点不统一 → 解析器做成可注册的小函数表，未知格式安全跳过。
- QuotaState JSON 与旧字符串共存 → 解析必须容忍，迁移非破坏。
