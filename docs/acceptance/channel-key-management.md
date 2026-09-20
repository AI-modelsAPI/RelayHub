# 单渠道 API Key 管理 + 网关轮询（本轮实现）

对照 `.superpowers/axonhub-replication-plan.md` 三-8 / 五-1（单 Key 增删禁用），本轮补齐。全部先写 RED 测试再实现。

## 实测（本机真实输出）

- `go test ./internal/api -run TestChannelKey -v` → 3 用例 PASS：
  - 创建响应**不含明文**、`secret_ref` 不外泄，值确实被密封进 secret store（server 侧可取回）。
  - 列表只回元数据，不含任何明文。
  - 禁用 + 删除：元数据消失且密封 secret 被一并清除（防孤儿密钥）。
- `go test ./internal/app -run TestResolveChannelCredential -v` → PASS：网关凭据解析在多个启用 Key 间轮询、**跳过 disabled**、全禁用后回落到 legacy CredentialRef。
- `go test -race ./... -count=1` → 29 包全绿无 race。
- `go vet` 空、`gofmt -l` 空、`make build` 通过、`make check-web` 同步。

## 改动

### 数据层
- `domain.ChannelKey`：id/channel_id/secret_ref/label/disabled/created_at（只存元数据，值在密封库）。
- 迁移 `006_channel_keys.sql`：channel_keys 表 + 按 channel_id 索引，注册进 db.go（版本 6）。
- repository：CreateChannelKey/GetChannelKey/ListChannelKeys/UpdateChannelKey/DeleteChannelKey，纳入 ResourceRepository 接口。

### API（internal/api/server.go）
- `POST /api/v1/channel-keys {channel_id,value,label}` — 值写入密封库（ref=`chkey:<channel>:<id>`），仅回元数据；DB 写失败时回滚已密封 secret。
- `GET /api/v1/channel-keys?channel_id=c` — safeChannelKey 只回 id/label/disabled/created_at，永不含 secret_ref/明文。
- `PATCH /api/v1/channel-keys/{id} {disabled,label}` — 启停/改备注。
- `DELETE /api/v1/channel-keys/{id}` — 删元数据并清除密封 secret。
- 均要求管理鉴权 + peer allowlist（沿用 authorize）。

### 网关（internal/app/wiring.go）
- 新增 `resolveChannelCredential`：优先在渠道的启用 Key 间 round-robin（atomic 计数），跳过 disabled；无 Key 时回落 legacy CredentialRef。原内联闭包抽为函数以便单测直接覆盖。

### 前端（web/app.js）
- 渠道编辑页新增「API Key 管理」区：列出现有 Key（含停用删除线 + 徽章）、启用/停用/删除按钮、添加输入（值+备注）。
- 提交走 channel-keys 端点；界面从不显示明文（POST 后即清空输入）。
- 中英文案 f_keys/f_key_label/btn_add_key/btn_enable/btn_disable/msg_no_keys 等。

## 安全说明
- 明文只在 POST 请求体出现一次，落地即密封；任何 GET/POST 响应都不回明文或 secret_ref（有回归测试断言）。
- 删除 Key 会同步清除密封 secret，避免孤儿密钥长期驻留。
- 网关轮询只用**启用**的 Key，停用即时生效（下一次请求不再选中）。

## 未完成（保留缺口）
- 单 Key 连通性测试（“测试单 Key”）：当前 channelTest 用渠道级凭据，未按单 Key 维度测。
- 展开行的端点/限流详情、额度显示。
- 模型映射独立多行对话框、模型级存档、模型图标上传。
- Tier 2 运行时下载真实 CDN 校验和（fail-closed 中）。
