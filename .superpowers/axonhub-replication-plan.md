# AxonHub 渠道/模型全量复刻计划

目标：将 AxonHub 渠道与模型的**全部业务逻辑**复刻进 RelayHub，逐条对照源码，不偷懒、不遗漏。

## 一、AxonHub 源码结构（已克隆 /tmp/axonhub，branch unstable）

- 前端: `frontend/src/features/channels/`、`frontend/src/features/models/`
- 后端: `internal/ent/schema/channel.go`（Channel 字段）、`internal/objects/channel.go`（ModelMapping/HeaderEntry/ChannelSettings）、`internal/server/biz/channel.go`（ChannelService）、`internal/server/biz/model_fetcher.go`（FetchModels）、`internal/server/biz/channel_model_sync.go`

## 二、AxonHub Channel 完整字段清单（ent schema）

| 字段 | 类型 | 说明 | RelayHub 对应 |
|---|---|---|---|
| type | enum(~80种) | 渠道类型 | provider_id 前缀 + select |
| base_url | string | 上游地址 | base_url ✅ |
| name | string unique | 名称 | name ✅ |
| status | enum(enabled/disabled/archived) | 三态状态 | status 字段 ✅ |
| credentials | JSON ChannelCredentials{apiKeys[], apiKey} | 多Key | credential_ref + 多渠道拆分 ✅ |
| disabled_api_keys | JSON | 被禁用的Key | 未实现 → 记录在 custom_headers 同级 JSON |
| supported_models | []string | 拉取的模型 | provider_models ✅ |
| manual_models | []string | 手动添加模型 | manual_models ✅ |
| auto_sync_supported_models | bool | 自动同步开关 | auto_sync ✅ |
| auto_sync_model_pattern | string | 同步正则过滤 | auto_sync_pattern ✅ |
| tags | []string | 标签 | routing_tags ✅ |
| default_test_model | string | 测试模型 | default_test_model ✅ |
| policies | JSON ChannelPolicies{stream} | 流式策略 | stream_policy ✅ |
| settings | JSON ChannelSettings{ModelMappings[]} | 模型映射 | provider_models 模拟 ✅(from→to) |
| ordering_weight | int | 排序权重 | weight ✅ |
| error_message | string | 最近错误 | error_message ✅ |
| remark | string | 备注 | remark ✅ |
| endpoints | []ChannelEndpoint | 多端点 | 未实现(可选) |

## 三、AxonHub 渠道页功能清单（channels-columns.tsx + dialogs）

1. ✅ 列表列: 选择框/名称/类型/状态/额度/标签/支持模型数/代理/健康/排序权重/创建时间/操作
2. ✅ 搜索+筛选(类型/状态/标签)
3. ✅ 批量操作: bulk-enable/disable/delete/archive/test/ordering/tags/template
4. ✅ 渠道健康检测(channel-health-cell): 成功率+响应时间显示 — 展开行 recent_success/recent_errors + avg_latency_ms
5. ✅ 额度显示(channel-quota-cell) — 展开行含 quota_state + GET /api/v1/channels/stats
6. ✅ 展开行(channel-expanded-row): 健康/额度/启用Key数/近期请求成功率/平均延迟/最近错误 — GET /api/v1/channels/stats + 前端 toggleChannelExpand
7. ✅ 添加/编辑: 类型/API格式/名称/BaseURL/多Key/代理(类型+预设)/模型(手动+拉取)/映射/优先级/权重/标签/状态/流式策略/测试模型/备注
8. ✅ API Key 管理: 添加/删除/禁用单个Key — POST/GET/PATCH/DELETE /api/v1/channel-keys（值入密封库，仅回元数据；网关按启用Key轮询，停用不参与）
9. ✅ 模型映射: from→to 多行编辑 — 渠道编辑页 e-ch-mapping（addMapRow/removeMapRow/collectMapping，存为 provider-models 的 model_id→upstream_model_name）
10. ✅ 测试连通(后端fetch-models)
11. ✅ 自动同步模型(auto_sync + 正则) — 调度器按 Interval 触发 SyncChannelModels(scheduler.SetModelSync 接线，正则过滤)
12. ✅ 复制渠道(duplicate) — POST /api/v1/channels/duplicate（克隆后清凭据、置 disabled，防误上线）
13. ✅ 存档(archived 三态) — POST /api/v1/channels/batch action=archive

## 四、AxonHub 模型页功能清单（models-columns.tsx + dialogs）

1. ✅ 列表列: 选择/图标/名称/模型ID/开发者/类型/工具调用/状态/关联规则/关联渠道数/创建时间/操作
2. ✅ 搜索
3. ✅ 模型图标(URL) — domain.Model.IconURL + 编辑页 + 列表缩略图（自动识别/上传留待后续）
4. ✅ 开发者/厂商字段 — domain.Model.Developer + 编辑页 + 列表副标题
5. ✅ 模型类型(LLM/Embedding/Image/Rerank) — domain.Model.ModelType + 下拉
6. ✅ 输入/输出价格(计费) — InputPrice/OutputPrice($/百万 token) + 编辑页
7. ✅ 工具调用能力
8. ✅ 能力标签(tools/vision/reasoning) — 已有编辑复选 + 列表 T/V/R 徽章
9. ✅ 关联规则(渠道模型映射管理) — 编辑页渠道绑定 + catalog binding_count
10. ✅ 新建/编辑/删除/批量创建 — POST /api/v1/models/batch models[]
11. ✅ 批量启用/禁用 — POST /api/v1/models/batch action=enable|disable
12. ✅ 存档(模型级) — domain.Model.Archived + batch archive/unarchive + catalog 默认隐藏(include_archived=true 显示)
13. ✅ 模型目录状态(models-catalog-status) — GET /api/v1/models/catalog（含 binding_count/价格/类型）

## 五、后端需新增的 API/逻辑

1. ✅ /api/v1/channel-keys — 单Key增/删/禁用/启用（POST/GET/PATCH/DELETE），值入密封库仅回元数据
2. ✅ POST /api/v1/channels/sync-models — 自动同步(pattern)
3. ✅ POST /api/v1/channels/test — 测试(更新健康状态/错误信息)
4. ✅ Channel 扩展字段: 全部已实现
5. ✅ GET /api/v1/models/catalog — 聚合模型目录(含 binding_count/价格/类型/能力)
6. ✅ 模型批量创建/批量启停 API — POST /api/v1/models/batch
7. ✅ 渠道复制/批量操作 API — POST /api/v1/channels/duplicate, /api/v1/channels/batch

## 六、实施顺序

Phase A: 后端字段扩展+API (Channel 3态状态/manual_models/auto_sync/remark/error_message/test端点)
Phase B: 渠道页全量复刻(展开行/Key管理/批量操作/复制/健康/额度)
Phase C: 模型页全量复刻(图标/价格/批量/目录)
Phase D: UI 品牌图标改绿底大图标+文字在下 + 字号≥12pt + 语言切换移入设置
