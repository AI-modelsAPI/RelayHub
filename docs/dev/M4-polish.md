# M4 开发文档：收尾加固（追踪/失败聚类 + 批量收尾 + WS 推送）

> 对应 `docs/ROADMAP-2026.md` M4。目标：可观测性补全 + 通用 CRUD 收到"够用"。**本里程碑是"够用即止"，不为追平 AxonHub 而扩张。**

## 1. 请求追踪 + 失败聚类
- 端点：`GET /api/v1/logs` 已有；增 `?group_by=error_class` → Top N 失败类别 + 各占比 + 涉及渠道。
- 前端：日志页顶部"失败 Top N"卡片，点类别过滤明细。
- RED→GREEN：造多种 ErrorClass 记录，验证聚类计数与排序。

## 2. 通用 CRUD 收尾（账本 Phase B/C 收"够用"）
- 渠道批量操作接线：多选已就绪，接 `POST /api/v1/channels/batch`（enable/disable/archive/delete）。前端加批量按钮 + 确认。
- 筛选补全：类型/标签筛选 + archived 状态选项。
- 归档语义：路由选择器拦截 `status=archived`（先复现路由反例再修）。
- **达标线**：日常管理不卡手即可。不追 AxonHub 的每个字段/弹窗。

## 3. WebSocket 实时推送
- 端点：`GET /api/v1/events`（WS）→ 推送签到完成、渠道掉线、自愈降权（来自 M1）等事件。
- 前端：事件驱动刷新渠道/账本，替代轮询；断线自动重连 + 退避。
- 不加依赖：手写最小 WS frame（或确认是否已有可复用实现），前端原生 WebSocket。

## 4. 验收
1. 各子项 RED→GREEN + 真实页面/连接验证。
2. 全量回归链通过。
3. 账本 `docs/axonhub-gap-ledger.md` 中 Phase B/C 项标注"够用"并关闭，不再扩张。

## 5. 不做（硬性）
- 不引入前端框架/构建链；不加图表库；不做多租户/计费/支付；不为追平 AxonHub 加无差异化字段。
- M4 完成后，通用 CRUD 冻结，后续投入只进护城河方向（M1–M3 的迭代）。
