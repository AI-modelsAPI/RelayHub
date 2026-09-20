# RelayHub 持久化与健壮性加固 — 设计规格

> 配套 `docs/product-roadmap.md`。仅设计，不含代码改动。评审日期 2026-09-19。
> 现状缺口来自独立只读架构审查，均带 `path:line` 证据；实施前逐项先写 RED 测试。

## 目标

在不改变六层资源模型（Provider→Channel→Model→ProviderModel→ModelGroup→Route）的前提下，
把数据完整性、凭据生命周期、后台任务健壮性、可观测性落到可验证状态。

## 架构与数据设计

### 权威数据边界（持久化分层）

- **强权威（强一致、必须持久、外键约束）**：`providers`、`channels`、`channel_keys`、`models`、`provider_models`、`model_groups`、`model_group_members`、`routes`、`secret_values`、`local_api_keys`。
- **弱权威/流水（最终一致、可定期修剪）**：`request_records`、`audit_events`、`cli_sync_records`、`config_backups`、`checkin_records`。
- **纯内存运行态（重启后可重建，禁止无差别强刷盘）**：`health.Registry`（熔断计数/半开/延迟评分）、`checkin.Scheduler` 的 `JobState`（倒计时/运行锁）、轮询 `atomic` 计数器。

### 持久化最优解（本地单写桌面应用）

现状 `internal/storage/db.go:51-60` 已是 WAL + `synchronous=NORMAL` + `foreign_keys=1` + `busy_timeout=5000`，方向对，但**"断电安全"表述夸大**（GAP-04）：WAL+NORMAL 只保证进程崩溃安全，OS 崩溃/断电可能丢最后一批 commit。最优权衡：

1. **控制面写（低频、关键）**：`AddChannelKey`/`SaveSecret`/`ConfigImport`/渠道增删 后显式 `PRAGMA wal_checkpoint(PASSIVE)` 或对该事务临时 `synchronous=FULL`，把关键配置钉到磁盘。
2. **数据面写（高频、弱关键）**：`request_records`/`audit_events` 维持 NORMAL，并引入内存 RingBuffer + 异步批写，吞吐与丢失风险解耦（GAP-08）。
3. **读写并发**：拆独立只读连接池（WAL 原生支持多读单写），管理 API/路由解析/健康查询走只读连接，避免被单写连接阻塞。
4. **主密钥**：落地 `PlatformKeyProvider` 的 macOS Keychain 实现，主密钥入系统凭据链，无桌面/无头环境 fail-closed 或降级带口令 Keyfile（GAP-05）。
5. **备份/还原**：用 `VACUUM INTO` 或 SQLite Backup API 生成事务自洽快照，还原前 `PRAGMA integrity_check` + 外键校验（GAP-10）。

## 缺口清单（有代码证据）

| GAP | 类别 | 位置 | 用户/系统后果 | 验收 |
|---|---|---|---|---|
| GAP-01 | 凭据生命周期 | `internal/api/server.go:1110-1120,1590-1600` | 删渠道只级联删 DB 行，未删 `secret_values` 密文 → 孤儿密文累积、导出会重打包幽灵凭据 | 建 channel+多 key 后 DELETE，断言 `secret_values` 对应 ref 物理删除 |
| GAP-02 | 导入导出完整性 | `internal/export/export.go:20-55`,`import.go:145-225` | 导出缺 `channel_keys`/`provider_models`/`model_groups(_members)`；密钥导入在事务外，中途失败即认证坏死 | 含关联实体导出→空库导入，记录数与外键全一致；SecretImporter 报错则整体回滚无残留 |
| GAP-03 | 调度持久化 | `internal/checkin/scheduler.go:30-48,145-185` | 状态纯内存，重启按 0~jitter 重排 → 已签渠道被重复签、周期错乱 | 写 finished_at=1h 前的记录，重启后 NextRunAt=上次完成+Interval，非 now+jitter |
| GAP-04 | 持久性表述 | `internal/storage/db.go:51-60` | "断电安全"夸大，关键写后无同步屏障 | 文档区分进程崩溃安全/断电安全；关键写后 checkpoint 被调用（单测） |
| GAP-05 | 凭据平台隔离 | `internal/secrets/keyring.go:14-55`,`app/wiring.go:120-128` | 主密钥明文文件与 DB 同目录，整目录拷贝/TimeMachine 即同时泄露密文+主密钥 | macOS 下主密钥不再明文裸露于数据目录，经 Keychain 读写 |
| GAP-06 | 后台并发 | `internal/checkin/scheduler.go:135-205` | 无界 `go func()`，上百渠道同触发 → 数百并发请求/浏览器，退避占锁数十秒 | 50 渠道同触发，活跃 worker ≤ 设定上限（3-5） |
| GAP-07 | 可观测性割裂 | `internal/health/state.go:30-145`,`gateway/gateway.go:190-250` | 熔断只在内存，`CreateHealthRecord` 生产零调用，`ListHealthRecords` 恒空；重启后坏节点复活接客 | 熔断后重启/查 stats 能反映历史健康与熔断原因 |
| GAP-08 | 单连接阻塞 | `internal/storage/db.go:65-66`,`gateway/gateway.go:230-245` | 网关响应主路径同步写 `request_records`，与后台争唯一连接 → 高并发阻塞/SQLITE_BUSY | 压测下写入不阻塞 HTTP 输出、无 BUSY |
| GAP-09 | 事务原子性 | `internal/api/server.go:1040-1070` | 建 key 先 `SecretStore.Put` 再 `CreateChannelKey`，DB 失败留孤儿密文 | 无效 channel_id 建 key 返 4xx/5xx 且 `secret_values` 无残留 |
| GAP-10 | 冷备一致性 | `internal/api/server.go:2160-2205` | 直接复制 `relayhub.db`（WAL 活跃数据在 -wal），拷贝大概率不完整；无还原 API | 高频写中备份，`PRAGMA integrity_check` 通过且含最新写入 |

## 实施顺序（与 roadmap 对齐）

- **P0**：GAP-02、GAP-01、GAP-09（+前端 I1/I3）
- **P1**：GAP-03、GAP-06、GAP-08（+前端 I4/I5/I6）
- **P2**：GAP-04、GAP-05、GAP-07、GAP-10（+差异化方向）

## 门禁

`go test -race ./... -count=1`、`go vet`、`gofmt -l` 空、`make build`、`make check-web`；改 web 后 `make sync-web` + `node --check` + `make test-web`；UI 项须真实浏览器操作核对。RED→GREEN，每项落 `docs/acceptance/<topic>.md`。
