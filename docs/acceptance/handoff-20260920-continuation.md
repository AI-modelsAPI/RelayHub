# 会话接手与续作计划（2026-09-20）

**接手来源**：Hermes 会话 `20260915_001055_703983`（最后 dump 2026-09-18T06:28，因模型 `DeepSeek-V4-Flash-0731` 无可用渠道而中断，503 max_retries_exhausted）。
**项目**：`/Users/zhangguojun/projects/Autoproxy`（RelayHub）。
**主计划**：`.superpowers/axonhub-replication-plan.md`（三/四/五节已全部 ✅）；**缺口账本**：`docs/axonhub-gap-ledger.md`（勾选 ≠ 完成，以账本为准）。

## 一、接手时确认的既有事实

会话中断点：渠道多选（Phase B 前置能力）已实现并通过独立只读审查（`deleg_6dc09a1d`，passed_with_defects）；正在做真实页面验证时，`browser_exec` 因 Chrome 占用真实 profile 锁失败，随后模型渠道不可用导致会话中断。

本机复核（2026-09-20）：

- `node --test tests/web/*.test.cjs` → 3/3 PASS。
- `go test ./internal/api/ ./internal/checkin/ ./internal/router/` → ok；`go vet ./...` 干净；`make check-web` → web assets in sync。
- relayhub 进程在 8790 端口运行中（PID 49026，注意：这是旧进程，新一轮开发后需重启再验证页面）。
- Git 仓库无任何 commit（全部 untracked），与会话中"未 commit/push"一致。

## 二、按缺口账本排列的待办（开发计划）

账本约束：每批先 RED 行为测试，再实现到 GREEN，再真实验证；状态/凭据修改需独立只读审查；不得因拆分取消原需求；Tier 2 真实下载不取消。

| # | 批次 | 内容 | 依据 |
|---|------|------|------|
| 1 | 渠道状态开关 | `toggleChannel` 只有调用无定义（web/app.js:301 调用点）；先补 RED 测试再实现，失败不得谎报 | 账本·主助手核对 |
| 2 | 归档语义统一 | `web/app.js:589` 对 archived 仍产生 enabled=true；`internal/router/selector.go:363` 只检查 Enabled/RoutingEnabled，未拦截 archived → 先复现路由反例再修 | 账本 |
| 3 | 渠道保存链路 | `web/app.js:625-638` catch 吞失败；编辑时覆盖 credential_ref、按名称重算 ID → 补保存失败与身份/凭据保持回归 | 账本 |
| 4 | 模型同步 | `internal/api/server.go:641-664` 逐条删建无事务、忽略错误、协议写死 openai-chat → 事务化、错误传播、保留手动映射；定时调度已部分（SetModelSync）需复核 | 账本 |
| 5 | 健康/额度 | 连通检测不等于模型可推理；成功率/延迟/额度前端展示与历史仍缺 | 账本 + deleg_66bf301f |
| 6 | 渠道批量操作接线 | 多选已完成（前置），但 bulk enable/disable/delete/archive/test 按钮未接 `POST /api/v1/channels/batch`（后端已存在 server.go:934） | 账本·Phase B |
| 7 | 筛选补全 | 类型筛选、标签筛选、archived 状态选项缺失（web/app.js:236-244） | deleg_66bf301f |
| 8 | Phase C 模型页 | 多选、厂商输入、分类、价格、批量创建/启停接线（后端 models/batch 已存在） | 账本 |
| 9 | Phase D UI | 多处字号 <12pt（16px）；重大布局调整须先确认设计 | 账本 |
| 10 | 多选审查缺陷 | deleg_6dc09a1d 提出：app.js:299-300 onchange 直赋覆盖/无时序保护（中）、checkbox 无 label 关联（低）、静态中文 aria-label（低）、vm 测试未模拟事件冒泡（中） | 独立审查 |
| 11 | 收尾 | 全部闭环后才进入独立审查 + 实机验收；Tier 2 真实 CDN 校验和、Docker/ARM64 实机、真实站点签到属环境依赖项 | BROWSER-PLAN / 收尾文档 |

## 三、本轮（接手第一轮）执行记录

### 批次 1：toggleChannel 状态开关（✅ 完成 2026-09-20）

- [x] 接手复核现状与测试基线
- [x] RED：`tests/web/channel-toggle.test.cjs` 4 用例复现 `toggleChannel is not defined`（4 fail）
- [x] 实现 `web/app.js` 增加 `toggleChannel`：乐观更新本地状态 + `PATCH /api/v1/channels/{id}`（仅 `{enabled,status}`，不改身份字段）+ 失败回滚并 toast + archived 拒绝写；`make sync-web` 同步 `internal/web/`
- [x] GREEN：`node --test tests/web/*.test.cjs` 7/7 PASS；`node --check` 通过
- [x] 全量回归：`go test -race ./...` 28 包 ok；唯一 `tests/docker` FAIL 经查为本机 8790/8787 已被运行中的 relayhub 占用（bind 冲突），用独立端口 `RELAYHUB_PROXY_ADDR=127.0.0.1:18787` 重跑该包 → ok，确认非本轮回归。`go vet`/`gofmt`/`make build`/`make check-web` 全部通过。
- [ ] 真实页面验证（8790，需重启到最新构建后点按行开关；旧进程仍为 9-17 构建）

**注**：`toggleChannel` 走 `PATCH` 而非 batch，因单行开关是独立交互；后端 `channelResource` PATCH 已支持。批量按钮（批次 6）另行接 `POST /api/v1/channels/batch`。

