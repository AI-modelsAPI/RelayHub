# AxonHub 复刻计划：收尾（本轮把剩余项全部做完）

`.superpowers/axonhub-replication-plan.md` 三/四/五节所有条目现为 ✅（`grep ⬜` = 0）。本轮补齐的项，全部先写 RED 测试再实现。

## 本轮新增

### 模型级：图标 / 存档
- `domain.Model` 增 `IconURL`、`Archived`；迁移 `007_model_icon_archive.sql`（版本 7），repository 读写纳入。
- `POST /api/v1/models/batch` action 扩展 `archive` / `unarchive`（archive 同时置 enabled=false）。
- `GET /api/v1/models/catalog` 默认隐藏 archived，`?include_archived=true` 显示；返回体含 `icon_url`/`archived`。
- 前端模型编辑页加 图标 URL + 归档复选；列表显示图标缩略图 + 归档徽章（archived 行半透明）。

### 渠道级：展开行（健康/额度/统计）
- `GET /api/v1/channels/stats?channel_id=c` 聚合：health_state、quota_state、rate_limit_state、error_message、key_count/enabled_key_count、近期请求数/成功/失败、平均延迟、最近健康检查时间。
- 前端渠道表每行加 ▸ 展开，按需拉取 stats 显示健康/额度/启用Key/成功率/平均延迟/最近错误。

### 自动同步模型（auto_sync + 正则）
- 抽出 `Server.syncChannelModels`（HTTP handler 与调度器共用一份 fetch+正则过滤+重建绑定逻辑），导出 `SyncChannelModels`。
- `checkin.Scheduler` 增 `SetModelSync` 回调 + `maybeAutoSync`：对 enabled && auto_sync 的渠道按 Interval 触发一次，正则来自 `auto_sync_pattern`；wiring 用 `sched.SetModelSync(apiServer.SyncChannelModels)` 接线。
- 单 Key 连通性测试：`channelTest` 支持 `key_id` 指定单个渠道 Key；不指定时优先启用 Key，回落 legacy CredentialRef。

## 实测（本机真实输出）

- 新增用例全 PASS：
  - `TestModelBatchArchiveHidesFromCatalog`、`TestModelCreatePersistsIconAndArchive`、`TestChannelStatsAggregates`（internal/api）
  - `TestSchedulerAutoSyncTriggersOnlyForEnabledAutoSyncChannels`（internal/checkin：只对 enabled&&auto_sync 触发、传对正则、Interval 内不重复触发）
- `go test -race ./... -count=1` → **29 包全绿，无 race，无 FAIL**。
- `go vet ./...` 空；`gofmt -l cmd internal tests` 空；`make build` 通过；`make check-web` = web assets in sync。

## 安全说明
- 单 Key 测试同样只在 server 侧解密，明文不回响应；key_id 必须属于该 channel，否则 404。
- 归档不删除数据，仅隐藏并停用，可 unarchive 恢复。

## 仅剩的非本计划缺口（需真实外部环境，无法本机完成）
- Tier 2 运行时下载的**真实 CDN 校验和**（当前 fail-closed，任何下载因无固定校验和被拒）。
- Docker 容器实机运行、ARM64 实机、真实第三方站点的线上签到闭环。
- 模型图标的“自动识别/文件上传”（当前支持 URL；上传需额外静态资源存储，未做）。
