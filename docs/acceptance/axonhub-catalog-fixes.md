# AxonHub 复刻计划：模型目录 + 渠道批量/复制（本轮实现）

对照 `.superpowers/axonhub-replication-plan.md` 未完成项，本轮补齐后端能力与前端表单，全部先写 RED 测试再实现。

## 实测（本机真实输出）

- `go test ./internal/api/ -run 'TestModelBatch|TestModelCatalog|TestChannelDuplicate|TestChannelBatch' -v` → 5 用例全 PASS。
- `go test -race ./... -count=1` → 29 包全 ok，无 race。
- `go vet ./...` 空；`gofmt -l cmd internal tests` 空；`make build` 通过；`make check-web` = web assets in sync。
- `go test ./internal/repository ./internal/storage -run 'RoundTrip|Migrat'` → ok（迁移 005 + 新字段往返读回）。

## 改动

### 后端
- `domain.Model` 新增 Developer / ModelType / InputPrice / OutputPrice。
- 迁移 `005_model_catalog.sql`：models 表加 developer/model_type/input_price/output_price 列，注册进 `db.go` migrations（版本 5）。
- `repository.go`：modelColumns、CreateModel、UpdateModel、scanModel 全部纳入新列。
- 新端点：
  - `GET  /api/v1/models/catalog` — 聚合模型 + provider-model binding_count + 价格/类型/能力。
  - `POST /api/v1/models/batch` — `models[]` 批量创建；或 `action=enable|disable|delete` + `ids[]` 批量启停/删除。
  - `POST /api/v1/channels/duplicate` — 克隆渠道；**故意清空 credential_ref 并置 disabled/status=disabled**，避免克隆体复用源凭据或未审直接上线。
  - `POST /api/v1/channels/batch` — `action=enable|disable|archive|delete` + `ids[]`；archive 走三态 status。

### 前端（web/app.js，已 sync 到 internal/web）
- 模型编辑页新增 开发者 / 类型下拉(llm/embedding/image/rerank/audio) / 输入价 / 输出价 字段，保存时提交。
- 模型列表副标题显示 developer 与非 llm 类型。
- 中英文案 f_developer/f_model_type/f_input_price/f_output_price。

## 安全说明
- channelDuplicate 不复制凭据、默认禁用：防止“复制渠道”变成把生产 key 静默扩散并立即生效。批量端点均要求管理鉴权 + peer allowlist（沿用 authorize/peerAllowed）。

## 未完成（明确保留为缺口）
- 单 Key 增删禁用 + 展开行（channel-expanded-row 的 Key/端点/限流详情）。
- 模型图标上传、模型级存档。
- 模型映射独立多行对话框（当前用渠道绑定行覆盖 from→to）。
- Tier 2 运行时下载的真实 CDN 校验和（当前 fail-closed，任何下载都会因无固定校验和被拒）。
