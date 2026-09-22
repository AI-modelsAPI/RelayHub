# RelayHub 全仓代码审计报告

> **审计日期：2026-09-22（Asia/Shanghai）**  
> **基线提交：`c4c332c538aa396008c04b83665b8b690dacc7bc`**  
> **工作分支：`arena/01a0c143-relayhub`**  
> **结论：不建议将当前基线作为功能完整、安全承诺已兑现的正式版本发布。**

本报告以当前代码和实际调用链为依据，不沿用旧报告或进度文档中的“已完成”结论。审计覆盖全仓范围，但**不等于所有执行路径均经动态验证，也不是无漏洞保证**。本轮交付为报告和只读证据脚本，没有修复业务代码。

## 1. 执行摘要

共归纳 **33 项发现：P1（高）13 项，P2（中）20 项；未确认 P0（严重）问题**。优先级同时考虑安全、数据完整性、核心功能和发布阻断，不是 CVSS 分数。

主要风险集中在：

1. **管理面存储型 XSS**：模型/渠道字段插入 HTML 时未完整转义，不可信上游模型列表可成为入口。
2. **配置与路由边界失效**：API 绕过 catalog 业务校验；fallback 环可无限递归；路由快照首次发布存在竞态。
3. **功能接线断裂**：extras API 没有注册；runaway guard 没有调用；加权策略缺少变化源；Codex 同步没有交付密钥。
4. **数据可迁移性与一致性不足**：导出遗漏路由依赖图；导入密钥不在事务内；自动同步先删后建且不刷新路由快照。
5. **代理/身份安全承诺不成立**：签到、模型探测、浏览器没有统一走渠道代理；代理 URL 的密码明文落库、回显、导出。
6. **协议与运行可靠性不足**：跨协议流式工具调用丢失；CDP 并发关闭可能 panic；Docker 映射端口与容器 loopback 监听不匹配。
7. **观测失真**：失败请求未记录、延迟计时过晚、新元数据只写不读，现有测试未覆盖这些完整链路。

### 风险边界

- 默认管理面及数据面绑定 loopback，是有效的暴露面缓解措施；不能据此将发现描述成“默认公网未授权攻击”。
- 网关生产接线启用了本地 API key 校验；管理面有 Host / Origin / Fetch Metadata 校验。
- 管理面的本地同源脚本仍具有强权限，**XSS 不会被这些浏览器边界保护阻止**。
- 管理员主动配置本地上游、代理访问私网是产品能力；没有把所有自定义 URL 笼统定性为 SSRF。
- 本次未访问真实供应商账户、未执行真实签到、未做破坏性压力测试。

## 2. 范围、方法与限制

### 2.1 基线规模

以下统计不包含本次新增报告和脚本：

| 项目 | 数量 |
|---|---:|
| Git 跟踪文件 | 227 |
| Go 文件 | 158 |
| 非 `_test.go` 的 Go 文件 | 75（含 1 个 testhelper）|
| 非测试 Go 代码行数 | 15,782（含注释/空行/testhelper）|
| Go 测试文件 / `Test…` 顶层函数 | 83 / 197 |
| `internal/` 一级模块 | 30 |
| SQL 迁移 / Shell 脚本 | 8 / 8 |
| 前端副本 | `web/` 与 `internal/web/`，3 对文件 |
| 平台代码 | Objective-C 壳、Swift 壳/Keychain、2 个 plist |

对生产源码按模块进行静态检查，重点追踪“管理 API → SQLite → catalog → resolver → gateway”和“scheduler → adapter/browser”的运行接线；测试侧检查文件/用例清单，并重点阅读协议、密钥轮换、持久化、浏览器、代理、前端和交付用例。文档与资源用于核对承诺、路径和交付，不对图标二进制进行逆向。

### 2.2 已执行验证

执行环境：Linux，Node.js `v22.22.3`，Python `3.11.2`，Python SQLite `3.40.1`。

| 检查 | 结果 | 能证明 / 不能证明 |
|---|---|---|
| `make check-web` | 通过 | 3 对资源字节一致；不是 UI 功能验收 |
| `make test-web` | 3/3 通过 | 现有测试只检查 HTML/CSS 字符串，未运行页面行为 |
| `node --check web/app.js` 及 embed 副本 | 通过 | JS 语法可解析 |
| 对 8 个 `.sh` 执行 `bash -n` | 通过 | Shell 语法检查，不代表脚本能端到端运行 |
| Python `plistlib` 解析 2 个 plist | 通过 | 格式有效，不证明 macOS 能正常启动 |
| `python3 scripts/audit-evidence.py` | 11 个探针完成 | 2 个 JS 行为探针、SQL 验证和定向源码检查，见第 7 节 |
| `python3 -m unittest discover -s tests/macos -p 'test_*.py'` | **发现 0 个测试** | 文件是函数式手动入口，不能把 `OK` 当成桌面测试通过 |

### 2.3 未能执行 / 尚未验证

- 环境没有 `go`、`gofmt`、`govulncheck`、`staticcheck`、Docker、macOS SDK/运行环境。
- `go version` 返回 `go: command not found`。向 `https://go.dev/dl/?mode=json` 和 `https://proxy.golang.org/golang.org/toolchain/@v/list` 请求工具链信息均遇到 `curl (35) SSL_ERROR_SYSCALL`。
- **没有执行** Go build、全量 Go test、race detector、vet、Go 覆盖率、Go fuzz、依赖漏洞扫描；没有将 Go 语义上的静态推断写成“已复现崩溃”。
- `go.mod`、Dockerfile、CI 均指定 Go `1.27`。本轮没有取得可用工具链或官方发布清单，**该版本的可获取性与兼容性待 CI/发布环境核实**，不擅自降低版本。
- 没有运行 Docker 网络可达性测试、真实 Chromium CDP 测试、macOS 两架构构建/生命周期/签名验证、真实 CLI 客户端互操作测试。
- Python SQLite 验证的是仓库 SQL，不是 Go `modernc.org/sqlite` 驱动、DSN、连接池或迁移函数的完整运行测试。
- JS 注入探针执行了实际 `rowHTML` 并解析输出中的事件属性；**没有在真实浏览器执行注入事件**。离线提示验证使用最小 DOM stub。
- 没有实时 CVE 数据库和依赖可达性结果，不能得出“依赖无漏洞”的结论。

### 2.4 证据等级

- **D（局部执行证据）**：执行仓库 JS 或 SQL 得到结果；范围见各条，不代表全栈复现。
- **S（静态确认）**：由明确的代码、调用点和缺失的保护得出。
- **R（运行待复核）**：竞态交错、进程/平台行为等还需目标环境运行确认；与 S 并用表示缺陷模式已定位，但未跑 race/平台测试。

## 3. 发现总表

| ID | 级别 | 发现 | 证据 |
|---|---|---|---|
| RH-01 | P1 | 前端未转义的 ID/tag 造成存储型 XSS 风险 | D/S |
| RH-02 | P1 | API 绕过业务校验，fallback 环及跨 Provider 绑定可入库 | D/S |
| RH-03 | P1 | CDP 收发/关闭缺少同步，可能进程级 panic | S/R |
| RH-04 | P1 | Docker 发布端口无法到达容器内 loopback 服务 | S/R |
| RH-05 | P1 | extras 管理接口整体未注册 | S |
| RH-06 | P2 | runaway guard 仅注入，没有执行 | S |
| RH-07 | P2 | 加权、轮询、随机策略在生产中退化为固定选择 | S |
| RH-08 | P1 | resolver 状态首次初始化/发布存在数据竞争 | S/R |
| RH-09 | P2 | `CustomHeaders` 没有持久化，身份配置丢失 | D/S |
| RH-10 | P1 | 多条出站路径绕过渠道代理和身份配置 | S |
| RH-11 | P2 | 凭据读取错误被吞掉，凭据类型/禁用语义不统一 | S |
| RH-12 | P1 | 跨协议转换静默丢失工具流、多模态等语义 | S |
| RH-13 | P1 | 配置导出缺少绑定、模型组及多密钥关系 | S |
| RH-14 | P1 | 导入配置与密钥非原子，skip 仍可能覆盖秘密 | S |
| RH-15 | P1 | 模型同步非原子且自动同步不更新运行快照 | S |
| RH-16 | P2 | request meta 字段只写不读 | D/S |
| RH-17 | P2 | 用量/信任/延迟指标漏报或失真 | S |
| RH-18 | P2 | 熔断器漏记最后一次失败，半开探测无并发门控 | S |
| RH-19 | P2 | AIMD 限流用错 key，等待不预留令牌 | S |
| RH-20 | P2 | HTTP 头部上限可绕过，网络超时/资源预算不足 | S/R |
| RH-21 | P2 | SOCKS5 缓冲数据丢失，隧道半关闭未透传 | S/R |
| RH-22 | P2 | 生命周期错误被吞，管理连接/后台任务未完整排空 | S/R |
| RH-23 | P1 | Codex 同步创建了 key，但没有把 key 交付客户端 | S |
| RH-24 | P2 | CLI 同步备份碰撞、失败回滚和校验不完整 | S |
| RH-25 | P2 | MCP 不是可互操作的持续 stdio 会话，多处功能占位 | S |
| RH-26 | P2 | 通用/声明式适配器仍直接发送 credential ref，声明式假成功 | S |
| RH-27 | P2 | macOS Keychain 实现未进入实际交付链 | S |
| RH-28 | P2 | Tier 2 浏览器下载/检测没有形成可用闭环 | S |
| RH-29 | P2 | UI 核心操作未接线，离线状态和数据展示不正确 | D/S |
| RH-30 | P2 | 审计记录覆盖不全，吊销失败仍返回成功 | S |
| RH-31 | P1 | 代理凭据明文存储、回显及随无密钥导出传播 | S |
| RH-32 | P2 | 历史查询/内存状态缺乏保留和容量上限 | S |
| RH-33 | P2 | 交付验收断链、版本/架构/发布流程不完整 | S |

> 所有编号均针对本次基线独立编号，不对应历史注释中的 P1/P2 编号。

## 4. 详细发现

### RH-01 · P1 · 前端存储型 XSS

**位置**：`web/app.js:41-46` 的 `rowHTML`、`:87-100` 的渠道渲染、`:122-125` 的模型渲染；`internal/web/app.js` 同样受影响。入口包括 `internal/api/server.go:668-737` 的上游模型同步。

- `title` / `meta` 经过 `esc`，但 `data-id="${id}"` 和 `${tag || ""}` 直接拼进 `innerHTML`。
- 上游模型 ID 被原样写入 `models.id` 并进入管理 UI；渠道 ID/status 也能经管理 API/导入进入该 sink。
- 探针 V01 用无外部请求的标记值确认：实际渲染输出会形成 `img` 的 `onerror` 属性，而非纯文本。

**影响/前提**：操作者同步不可信模型列表、导入不可信配置，或存在可写配置的本地进程；随后在 UI 查看相应列表。恶意脚本可获得管理页面同源权限，例如修改上游、创建本地 key、发起带密码的秘密导出。不能说默认公网可直接写库。

**建议**：用 `createElement` / `textContent` / `dataset` 构造节点；所有字符串按 HTML 属性/文本上下文编码，状态做枚举校验；添加严格 CSP 作为纵深防御。

**验收**：真实浏览器加载含引号、`<img…>`、实体、Unicode 的渠道/模型 ID 和 status，确认无新事件节点、无脚本执行，且合法模型仍可选择。覆盖两个前端副本。

### RH-02 · P1 · 业务约束没有贯穿 API → 路由

**位置**：`internal/app/wiring.go` 的 `api.Config{Repo: repo}`；`internal/api/server.go:1736-1787`、`:2350-2403`；`internal/catalog/service.go` 的 ProviderModel/Group 校验；`internal/router/selector.go:273-288`；`internal/repository/repository.go:481-610`。

- API 写入 SQL repository，而不是带业务校验的 catalog service。`validateGroup` 不检查 fallback 环，数据库外键只检查被引用对象存在。
- `selectGroupModel` 只执行 `visited[groupID] = true`，从未读取 visited。无可用成员时，self-cycle 或 A→B→A 递归不终止，也没有深度/context 检查。
- ProviderModel 的 `provider_id` 与所绑定 Channel 的 `provider_id` 不一致也可保存；catalog 中的归属校验在这条写路径没有执行，resolver 亦不复核。
- V06/V07 已用实际迁移 SQL 确认这两类非法关系可以入库；没有执行栈溢出。

**影响/前提**：误配置或导入后，请求对应空模型组可能导致堆栈耗尽并使 Core 退出；错误归属使禁用策略、路由和账单归因不一致。

**建议**：统一应用服务校验入口，SQL/事务层补充关系约束；resolver 防御性检查 visited、最大深度和 context；禁止跨 Provider 绑定、冲突路由和未知策略。

**验收**：真实 API 拒绝自循环/多节点环/跨 Provider；直接污染数据库时 resolver 返回错误而非递归失控。

### RH-03 · P1 · CDP 并发与关闭竞态

**位置**：`internal/browser/cdp/client.go:196-207,211-252,255-292,334,360-384`。

- `dispatch` 在锁内获取 pending channel，解锁后 `ch <- resp`；`Close` 可以在二者之间关闭该 channel，形成 send-on-closed-channel panic。该 goroutine 没有恢复边界。
- 多个 `Call` 与 readLoop 回复 Ping 共用 `bufio.Writer`，`writeFrame` 没有写锁，帧可能交错。
- 帧长度直接从对端 64 位值转为 `int64` 后 `make([]byte, payloadLen)`，没有符号/上限检查；畸形 CDP peer 可触发 panic/OOM。

**影响/前提**：正常取消/浏览器退出与响应到达交错即可触及关闭竞态；超大帧分配需要异常或被替换的本地调试端点。**尚未运行 race detector。**

**建议**：单写协程或写锁；统一 pending 所有权，不由 Close 关闭可能被发送的 channel；有界且取消感知的消息投递；限制帧/消息长度并正确处理 continuation frame。

**验收**：mock WebSocket 下并行 Call/Ping/Close 的 `go test -race`，负数/巨量长度、分片、重复响应、取消测试；确认不 panic、不死锁。

### RH-04 · P1 · Docker 网络不可达

**位置**：`docker-compose.yml:11-15`；`internal/app/wiring.go` 默认 `127.0.0.1:8787/8788/8789/8790`；`cmd/relayhub/main.go:37-85`、`management.go`；`tests/docker/smoke.sh`。

Compose 使用 bridge 网络和端口发布，但四个服务都监听**容器自己的 loopback**。宿主端口转发到容器网卡 IP，不能到达这些 socket。管理地址 CLI 又明确拒绝非 loopback；其余三个监听地址没有 CLI/env 接线。现有 Docker smoke 只在 `docker exec` 内运行 healthcheck，恰好绕过外部可达性问题。

**影响**：README 的 `docker compose up -d` 即使容器健康，也不能据此获得可用的宿主管理面/代理/网关。

**建议**：设计明确的容器网络模式与认证边界；非管理面可配置监听地址，宿主发布默认 loopback。管理面如需容器网卡监听，必须同时落实 bearer、peer allowlist、速率限制和 Host/Origin 策略，不能仅改成 `0.0.0.0`。8787–8789 当前发布到宿主所有网卡，未来修复监听时须避免顺带暴露无认证代理。

**验收**：在宿主及同 Compose 网络另一容器检查四个端口与鉴权，分别验证允许/禁止的来源。此项网络运行复现待 Docker 环境。

### RH-05 · P1 · extras API 没有挂载

**位置**：`internal/api/server.go:261-300`；`internal/api/extras.go:20-51,275-280`。

`extras()` 实现了 usage summary、verify、identity、sessions、lab、routes explain、MCP，但没有任何 `mux.Handle…(..., s.extras)`。`WithControlPlane` 只赋字段，不注册路由。大部分请求落到静态 FileServer；`/api/v1/routes/explain` 则被资源路由当作 route ID `explain`。这不是响应字段小差异，而是整个接口没有进入目标 handler。

**影响**：UI 依赖的数据、MCP HTTP 转发和多项 M1–M3 能力不可用。V08 已检查该注册表。

**建议/验收**：明确注册每个 method/path，未知 API 不落静态资源；通过**生产 `Handler()` / 真实 listener**测试所有 extras 端点的成功、错误、鉴权、Origin，不只直接调函数。

### RH-06 · P2 · runaway guard 没有执行

**位置**：`internal/app/wiring.go` 创建 `guard.New()` 并传入 gateway；`internal/gateway/gateway.go:173-188`；`internal/guard/runaway.go:28-57`。

全仓生产路径没有 `Guard.Trip` 调用。重复 payload 检测仅在 guard 单元测试中执行。V09 确认字段存在但网关没有调用；`docs/dev/STATUS-2026-09-21.md` 中“已接线”描述不准确。

**影响**：用户依赖的循环成本保护实际上不存在。

**建议/验收**：在产生上游费用前调用保护，以用户/key+session 为范围，明确阈值和返回码；端到端验证同 session 超阈值时上游调用数不再增加，且不同会话不互相封禁。

### RH-07 · P2 · 负载均衡策略退化

**位置**：`internal/gateway/gateway.go:265-270`；`internal/app/wiring.go` 的 resolver 初始化；`internal/router/selector.go:297-312,437-498`。

网关没有填 `router.Request.Source`，生产 resolver 没有设置 `Rand`；weighted / round-robin / random 使用的 source 恒为零，通常固定选择第一个候选。模型组策略也用 `req.Source`。另外，组优先级过滤的循环检查的是已排序数组的首元素，首元素本来就等于 bestPriority，不会删除较低优先级成员。

**影响**：热点渠道被打满，权重和组优先级行为与配置不符。

**建议/验收**：为渠道/模型组配置线程安全的策略状态，随机策略使用真实随机源；先筛选最优优先级再加权。100 次不带 session 的真实网关请求验证分布、权重与 fallback，不仅在 selector 测试中人工传 Source。

### RH-08 · P1 · resolver 首次状态发布竞争

**位置**：`internal/router/selector.go:71-105,108-141,171-173`；`internal/app/wiring.go` 的 `refreshResolver`。

`ensureState` 在没有锁/Once/原子的情况下读写 `r.state`。生产初始化仅填导出 map，第一次 API 变更才创建 state；并发网关请求通过值接收器复制 Resolver、读取 state。state 内的 RWMutex 不能保护这个指针的首次发布。两个刷新也可能各建一份状态并丢失其中一次更新。

**影响**：数据竞争、快照更新丢失；具体交错需 race 验证。Load 从多个 SQL 查询组装快照也不保证读一致性，应一并设计版本化发布。

**建议/验收**：构造时初始化不可变快照，用原子指针或统一锁发布；避免值接收器复制包含可变状态的对象。真实运行接线下并发修改资源/发网关请求跑 `-race`，验证最新版本不回退。

### RH-09 · P2 · CustomHeaders 不落库

**位置**：`internal/domain/entities.go:26`；`internal/repository/repository.go:222-316`；8 个迁移；`internal/identity/bundle.go:24-46`。

Domain/API 接受 `custom_headers`，但 channels schema、INSERT、UPDATE、Scan 均没有该字段。UI/API 可能回显提交对象，下一次读取/刷新快照即丢失，不必等到进程重启。UA、时区及自定义认证头均受影响。V04 确认 schema 缺列。

**建议/验收**：添加迁移与完整读写/round-trip 测试；敏感头不要作为普通 JSON 明文持久化，改为 secret ref；重读/重启后实际上游收到期望的非敏感头。

### RH-10 · P1 · 渠道出口与身份配置被绕过

**位置**：`internal/adapter/base.go:261-320` 的 `doReq`；`internal/api/server.go:564-581,1329-1368`；`internal/browser/executor.go:24-30,127-149`；`internal/identity/bundle.go:48-63`；`internal/gateway/gateway.go:145-155`。

- `CallWithAuth` 的实际账号请求使用 `b.Client`，只有 refresh/status 等少数路径使用 `clientForChannel`。
- 模型拉取/连通性测试自行创建直连默认客户端，不读取渠道 ProxyURL 或身份头。
- 浏览器 CheckinRequest 不携带代理/UA/时区，启动参数及 CDP 初始化没有应用它们。
- `identity.ApplyRequestHeaders` 没有生产调用点；不合法 ProxyURL 被忽略，可能静默退回默认出站行为。

**影响/前提**：用户配置代理希望绑定账号出口；实际部分请求暴露默认出口，或在不一致 IP/UA 下触发上游账号风控。没有断言所有环境都会公网直连，默认 transport 也可能使用环境代理。

**建议/验收**：统一渠道 HTTP client/transport factory 和浏览器身份配置；显式代理非法或不可用时 fail closed。给每条出站路径设置只能通过 mock proxy 到达的目标，断代理后不得绕路成功。

### RH-11 · P2 · 凭据错误/类型处理不一致

**位置**：`internal/app/wiring.go:347-385`；`internal/adapter/base.go:67-103`；`internal/api/server.go` 的 fetch/test/sync key 选择。

`resolveChannelCredential` 将数据库和解密失败当作空 token 或 legacy fallback，几乎不返回实际错误；选中的 key 无法解密也不尝试其他有效 key。所有 channel keys 被禁用后回退 legacy 是**现有测试明确要求的行为**，因此不是偶然漏判，但会让“禁用全部 key”的操作语义混淆。账号 JSON secret（含 token/cookie）在 adapter 中会解析，网关/模型探测却把整个 JSON 转成 bearer 字符串。

**影响**：凭据损坏被伪装成上游 401；可能继续使用旧凭据；账号 JSON 可能将不该出现在推理请求里的 cookie 一并送至上游。

**建议/验收**：区分账号会话 secret 与推理 API key；数据库/解密错误 fail closed；明确 legacy fallback 策略并向 UI 展示。测试缺失/损坏 secret、全禁用、JSON credential、多个有效 key 的全部分支。

### RH-12 · P1 · 跨协议转换静默丢失语义

**位置**：`internal/gateway/stream.go:164-356`；`internal/gateway/transform.go:84-409`；`internal/router/selector.go:328-339`。

- OpenAI→Anthropic 流只提取 `delta.content`，不转换 `delta.tool_calls`；反方向忽略 `tool_use` start 和 `input_json_delta`，却可能仍返回 `tool_calls/tool_use` 结束原因。
- 请求转换不完整支持 image/content 数组、thinking/reasoning、块级 cache_control；OpenAI→Anthropic 未完整转换 tool_choice。能力标记放行不代表转换器能保留对应能力。
- `/v1/embeddings` 被归为 openai-chat，路由允许转到 anthropic-messages，endpoint 参数并没有阻止这一不成立的转换。
- OpenAI 流 EOF 无 finish_reason 时补发 `end_turn/message_stop`，可能把截断伪装成正常完成。

**影响**：coding agent 收不到工具参数，多模态内容被丢弃；请求已产生费用但结果不可用。

**建议/验收**：以端点+能力定义明确支持矩阵，无法无损转换时返回不支持，不静默降级；实现按 index/id 管理工具流的状态机，测试双向多工具分片、图片、推理、缓存、embeddings、异常 EOF 和错误事件。

### RH-13 · P1 · 导出包不是完整配置快照

**位置**：`internal/export/format.go:26-33`；`internal/export/export.go:21-58`；`internal/export/import.go:136-210`。

PackageData 只有 Providers/Channels/Models/Routes/Secrets，没有 ProviderModels、ModelGroups、Members、ChannelKeys。恢复到空库时，没有绑定的模型不可路由；包含 GroupID 的 route 会因缺组而外键失败；多 key 的密文即使导出了也丢失绑定关系。

**影响**：用户依赖“备份成功”迁移/重装后无法恢复运行拓扑。

**建议/验收**：升级版本化包格式，覆盖完整配置依赖图，按外键顺序导入；区分配置、账号 secret、本地访问 key 和历史日志的迁移政策。用包含 group fallback、channel 多 key 和路由的真实配置导出到全新库，验证可发一次网关请求。

### RH-14 · P1 · 导入密钥与配置事务分离

**位置**：`internal/export/import.go:149-223`；`internal/api/server.go` 的 `SecretImporter` 回调。

配置事务提交后才逐个 Put secrets；中途失败会保留已导入配置及部分新秘密。ConflictSkip 只控制配置实体，decryptedSecrets 仍全部写入，可覆盖被跳过实体正在引用的 secret。checksum 是无密钥 SHA-256，只提供意外损坏检测，不能认证包来源。

**建议/验收**：先完整解密/校验依赖和冲突，再用同一事务写密文与实体；secret 采用一致冲突策略并处理 ref 重映射。注入第 N 个 secret 写失败，断言数据库完全不变；skip 后原凭据必须保持。文档明确 checksum 不是签名。

### RH-15 · P1 · 模型同步破坏原绑定且不刷新自动快照

**位置**：`internal/api/server.go:668-744`；`internal/app/wiring.go` 的 `sched.SetModelSync`；`internal/checkin/scheduler.go:161-182`。

同步先删掉全部 channel-specific bindings，再逐个创建；List/Delete/CreateModel 多处错误被忽略，ProviderModel 创建失败也只少计数，最后返回 nil。新 binding 一律 `openai-chat`；新 Model 的能力布尔值全为 false，无法代表上游实际能力。定时器调用的 `SyncChannelModels` 没有 `notifyConfigChange`，只有 HTTP handler 的手动同步会刷新 resolver。

**影响**：临时 DB 错误或响应异常可丢掉定制绑定；自动同步后列表看似更新，网关仍用旧路由；部分工具/视觉请求被错误拒绝。

**建议/验收**：先验证列表与协议、计算 diff，事务性替换，保留人工能力/transform 元数据；提交后统一更新版本化快照，失败显式报告。故障注入验证无数据损失，定时同步后无需重启即能调用新模型。

### RH-16 · P2 · 元数据只写不读

**位置**：`internal/repository/repository.go:783-836`；`internal/storage/migrations/008_request_record_meta.sql`。

INSERT 已包含 `ttft_ms/cache_read_tokens/cache_write_tokens/finish_reason/upstream_model`，但 SELECT 和 scanRequest 仍只有旧 12 列。所有从 repository 读取的这些字段都成为零值。V05 实际写入 81 个 cache tokens 后执行源码中的 SELECT，确认查询结果不包含字段。

**建议/验收**：统一列清单及 scanner，补充这五列的创建→读取→重启往返测试；API usage/explain 的结果必须和入库值一致。

### RH-17 · P2 · 观测数据偏向成功并低估延迟

**位置**：`internal/gateway/gateway.go:388-451`；`internal/gateway/stream.go:42-52,99-146`；`internal/gateway/transform.go:414-440,657-709`；`internal/verify/score.go:31-68`。

- 失败、取消、路由错误及部分坏流直接 return，Recorder/Verify 只在成功路径触发。verify 的 429/5xx 扣分在该接线下无法正常收到样本。
- `startReq` 在 Upstream.Do 返回成功头部后才建立；TTFT 也从读流时开始，不包括之前的网络、排队和等待响应头的时间。
- 非流记录从改写后的 response 取 model，覆盖真实 upstream model；verify 又把上游名与逻辑 ModelID 比较，会对合法别名映射误报。
- `absorbUsage` 没有从 Anthropic `message_start.message.usage` 提取输入/cache tokens；跨协议响应也会损失 cache 字段。

**影响**：成功率、信任分、缓存收益和延迟不能作为可靠路由或成本依据。

**建议/验收**：请求入口记时，统一 finalize 记录终态并区分每次 attempt；从原始 upstream 记录元数据，与期望 upstream mapping 比较；完整解析各协议 usage。使用可控延时/429/5xx/取消/SSE fixtures 验证全部指标。

### RH-18 · P2 · 熔断与健康状态没有闭环

**位置**：`internal/gateway/gateway.go:323-389,401-405`；`internal/health/state.go:45-67,111-169`。

最后一次尝试失败时，不执行 RecordFailure；`MaxAttempts=1` 时失败完全不计入熔断。成功收到响应头即以 latency=0、successRate=1 标记健康，即便后续 body 坏掉。Available 在 cooldown 到期后直接放行全部并发请求，AllowProbe 没有生产接线。quota-aware 所需额度数据也没有看到由 adapter Balance 向 registry 更新的生产闭环。

**建议/验收**：所有 attempt 统一记录健康结果；成功应以有效响应为准；半开只允许有界探测并释放探测槽；接入真实额度更新。测试 maxAttempts=1、连续终次失败、半开并发及 malformed body。

### RH-19 · P2 · AIMD 等待与观察不使用同一个限流对象

**位置**：`internal/gateway/gateway.go:294-326`；`internal/ratelimit/aimd.go:70-97`。

Wait 用上一次响应的 CredentialKeyID，第一轮退回 ChannelID；Observe 用刚选出的 key ID。新请求并未按它将使用的 key 等待，切换渠道时又可能继续等旧 key。令牌不足返回等待时长但不预留令牌，多个等待者会同时被放行；等待超过 2s 且无 fallback 时仍可能继续请求。

**建议/验收**：先选择凭据、再以相同 key 做可取消的 reservation，发送前确认资格；明确无 fallback 的 429/排队行为。并发模拟 429，验证真实上游调用间隔和不同 key 的隔离。

### RH-20 · P2 · 网络输入上限与超时不充分

**位置**：`internal/proxy/http_proxy.go:154-174`；`internal/app/wiring.go` 的两个 `http.Server`；`internal/gateway/gateway.go:145-155,245-250`；`internal/browser/cdp/client.go:127-164`。

- HTTP proxy 先 `ReadBytes('\n')` 再检查 header.Len，大量不带换行的输入可让分配超过 MaxHeaderBytes。
- 管理/网关 Server 没有 ReadHeaderTimeout/IdleTimeout 等配置；生产 gateway RequestTimeout=0，默认 http.Client 无整体超时。
- 每次带渠道代理的网关请求都新建 Transport，无 idle timeout/回收策略，长期复用资源不受控。
- CDP 的 DialContext 只限制拨号；连接建立后的握手读写没有 deadline，不会自然跟随 context 超时。

**影响/前提**：默认主要是本地连接或上游异常导致资源耗尽；不把默认配置描述成公网慢连接漏洞。

**建议/验收**：在读取之前施加字节上限；配置分阶段超时、并发/连接预算及可复用 transport，流式请求采用 idle timeout 而非盲目固定总时长；测试无换行头、慢握手、静默上游和大量并发。

### RH-21 · P2 · 代理隧道的数据与 EOF 语义缺失

**位置**：`internal/proxy/socks5_proxy.go:88-144`；`internal/proxy/proxy.go:298-338`；`internal/proxy/http_proxy.go:151,237-278`。

SOCKS5 用 bufio.Reader 解析握手和 CONNECT，但隧道直接读取原始 c；若 CONNECT 与首段应用数据在同一 TCP 写中到达，bufio 已预取的数据不会转发。halfClose 只识别 `*net.TCPConn`，调用方实际传入 bufferedConn/deadlineConn，CloseWrite 不会到达底层 TCP。

**影响**：首包偶发丢失；依赖请求 EOF 才响应的协议等待到 idle timeout。

**建议/验收**：SOCKS 也透传缓冲 reader；通过 CloseWrite/CloseRead 接口或包装器显式委托底层半关闭。测试合并握手+payload，以及上游收到 FIN 后才返回数据的双向隧道。

### RH-22 · P2 · 启停错误和后台任务没有被完整管理

**位置**：`internal/app/wiring.go:395-419`；`internal/checkin/scheduler.go:104-182,184-230`；`internal/app/app.go:91-111`。

Start 在 goroutine 中忽略 Serve/Start 错误并立即成功。管理 http.Server 只存在于局部变量，Shutdown 只关 APIListener，未调用管理 server 的 Shutdown，因此不能等待已接入请求。Scheduler.Stop 等待 runLoop 退出，却没有等待 RunNow/autoSync goroutine；多个清理错误一律丢弃后关闭数据库。

**影响**：停止期间的任务可能访问关闭数据库、丢记录或留下活动连接；运行中服务退出不能准确上报。

**建议/验收**：保留全部 server，建立 runtime 子 context、WaitGroup/error channel；按停止接入→取消/排空任务→关浏览器/代理→关 DB 顺序退出，聚合错误。慢签到、慢管理请求、立即 start/stop、listener 异常的生命周期测试。

### RH-23 · P1 · Codex 同步无法完成认证

**位置**：`internal/clisync/service.go:102-119,172-186`；`internal/clisync/codex/codex.go:56-129,144-158`。

Service 创建真实本地 API key，Codex Syncer 却不读取 desired.APIKey，只写 `env_key = "RELAYHUB_API_KEY"`，不设置环境/凭据文件，也不在 diff 交付此 key。Verify 仅检查 model_provider，随后 API 返回 success。V11 确认这一源码路径。

**影响/前提**：用户没有自行配置另一个有效 RELAYHUB_API_KEY 时，“一键同步成功”后客户端仍不能认证；新建的 key 成为孤立授权。

**建议/验收**：使用目标 Codex 版本支持的安全认证配置/启动环境交付方式；Verify 验证 endpoint/model/key 可用性；全新 HOME/env 下启动实际 Codex 或等价 wire 客户端成功发请求，并验证 key 可吊销。

### RH-24 · P2 · CLI 同步的备份、回滚与校验不足

**位置**：`internal/clisync/backup.go:19-39`；`internal/clisync/service.go:86-138,209-218`；`internal/clisync/hermes/hermes.go:140-212`；`internal/clisync/claude/claude.go:146-170`。

- 备份文件名只有 basename+秒级时间，使用可覆盖 WriteFile；同秒重复写或不同目录同名文件会覆盖备份。
- Hermes 先写 YAML 再写 `.env`，只备份 YAML；Apply 失败直接返回，不恢复已写文件。Verify 失败也不会恢复 `.env`，原先不存在的文件不会删除。
- rollback 用普通 WriteFile，未复用原子/防 symlink 写；恢复前不验证已计算的 SHA256。
- Preview 会创建永久有效 key；反复预览/失败会留下未使用授权。
- Verify 多数只检查一个 provider 或 URL，不验证 key、model、base_url 整体期望。

**建议/验收**：用目标路径哈希+随机 ID 的独占备份；多文件 staging/commit/rollback；失败撤销新 key，preview 不发放可用授权；严格校验实际结果。注入第二文件写失败、验证失败、并行同秒更新等故障。

### RH-25 · P2 · MCP 与部分控制面功能是占位实现

**位置**：`cmd/relayhub/mcp.go:15-40`；`internal/mcp/rpc.go:11-90`；`internal/clisync/claude/claude.go:104-114`；`internal/api/extras.go:90-102,172-199,262-268`。

- stdio `ReadAll(os.Stdin)` 等 EOF 才转发一次，无法承载客户端保持 stdin 打开的逐行、多请求会话；没有 initialize/initialized 协商，tools 缺 inputSchema、错误缺 JSON-RPC code，tool result 也不是标准内容块结构。
- clisync 写 `RELAYHUB_MGMT`（值还是 gateway URL），main 读取的是 `RELAYHUB_MANAGEMENT_ADDR`；自定义管理端口无法由该配置正确传递。桌面内 Core 也不一定作为 `relayhub` 在 PATH 上。
- `RunCheckin` 仅根据 scheduler 是否存在返回 ok，没有执行；probe 只读被动分；lab replay 只返回说明，不调网关。HedgeAfter 在本基线连字段也不存在，和旧进度文档不符。

**影响**：即便修复 RH-05，仍不能宣布 MCP/主动探针/回放已完成。

**建议/验收**：实现标准持续会话及生命周期，统一地址配置；真实执行功能或明确返回 unsupported。使用真实 MCP 客户端从 initialize 到多次 tools/call 验证，并断言 run_checkin 实际触发任务。

### RH-26 · P2 · 通用/声明式适配器违反凭据与成功判据契约

**位置**：`internal/adapter/generic.go:48-71`；`internal/adapter/declarative.go:89-113,145-168`；`internal/app/wiring.go` 的 adapter registry。

Generic.Models 与 Declarative.executeStep 直接 `Bearer + channel.CredentialRef`，未解析 SecretStore。Declarative.CheckIn 对 HTTP 成功但 `success:false` 或非 JSON 响应也返回 Success:true。声明式 adapter 目前没有在生产 wiring 注册/加载；管理模型获取走另一实现，因此应把这部分标记为**潜在接入缺陷**，而非宣称当前所有签到都假成功。

**建议/验收**：统一 SecretResolver 并严格检查业务成功/人工验证信号；补齐声明式加载入口前先加安全契约测试。HTTP 200 的失败 JSON、登录 HTML 和验证码页必须失败/need_manual，绝不标已签。

### RH-27 · P2 · macOS Keychain 仅有源码，没有实际使用

**位置**：`internal/app/wiring.go` 的 `NewFileKeyProvider`；`desktop/macos/KeychainStore.swift`；`scripts/build-macos.sh:34-45`；`docs/security.md`。

所有平台均使用 `master.key` 文件。macOS 构建只编译 Objective-C `main.m`，不编译/桥接 KeychainStore.swift，也没有给 Go provider 注入 Keychain 的渠道。README/security/UI 对 Keychain 的描述超出现状。

**影响**：备份数据目录同时包含密文数据库及解密主密钥，不能按“macOS 主密钥在系统钥匙串”评估威胁模型。

**建议/验收**：真正接入平台 provider、迁移旧 key，并明确 Keychain 不可用时的 fail-closed 策略；或者先纠正文档。在 macOS 验证新安装/迁移/钥匙串锁定/拒绝访问及备份恢复。

### RH-28 · P2 · Tier 2 浏览器不可用闭环

**位置**：`internal/browser/runtime_download.go:51-56,87-140,140-184`；`internal/browser/detect.go:48-122`；`internal/api/server.go:2051` 附近 browser handler。

所有 pinnedChecksums 为空，默认下载会正确拒绝；没有生产下载调用点/同意流程，Detect 只查系统浏览器，不查 DownloadedRuntime。extractShell 只取可执行文件，未解压浏览器所需其他资源；这点需要真实归档运行验收。Docker 镜像也没有安装 Chromium。

**建议/验收**：保留校验失败拒绝这一正确行为；补齐合法 checksum、完整安全解包、下载同意、运行检测接线和平台支持矩阵。无浏览器环境应准确显示 manual，不宣传自动下载可用；真实包执行页面导航，不只 mock `--version`。

### RH-29 · P2 · UI 核心操作和在线状态不完整

**位置**：`web/index.html` 的 `ch-new/md-new/lab-seg/ag-rows`；`web/app.js:187-301`。

渠道/模型“新建”、Agent sync、identity 编辑、lab 操作等只有显示结构，没有对应事件/API 操作。`Promise.allSettled` 在全部请求失败时也 resolve，boot 无条件移除 offline class；V02 已执行确认。字段使用 `u.model`、`ch.tags` 而后端提供 `model_id`、`routing_tags`；签到 POST 不检查 HTTP 状态，错误被吞掉。

**影响**：新安装无法仅通过 UI 配置完整模型调用链，服务离线却显示正常。

**建议/验收**：补齐受支持操作的状态机；未实现功能禁用并解释；以 health/overview 成功判定在线，逐请求展示失败。浏览器测试涵盖全新环境创建拓扑→同步 CLI→发请求→看用量，以及所有 API 离线/401/500。

### RH-30 · P2 · 审计与吊销结果不能可靠反映实际状态

**位置**：`internal/api/server.go:484-493,1612-1930,2475` 附近；`internal/auth/local_keys.go:99-112`；`internal/audit/audit.go`。

Provider/Channel 有 auditEvent，但普通 Model、ProviderModel、Group、members、Route CRUD 等没有对应审计调用；auditEvent 忽略持久化错误。LocalKeyService.Revoke 无 error 返回并吞掉 backend.Delete 失败，API 仍返回 revoked。

**影响**：缺少关键变更证据；数据库失败时操作者可能误以为泄露的 key 已吊销。

**建议/验收**：变更事件与重要控制面写采用明确事务/失败策略，吊销必须返回并验证持久化结果；审计失败应可观测。覆盖每个变更 endpoint，模拟 DB 锁定/关闭，断言不能假报 revoked。

### RH-31 · P1 · 代理密码绕过 SecretStore

**位置**：`internal/domain/entities.go:45` 附近 ProxyURL；`internal/repository/repository.go:243-314`；`internal/api/server.go:2496-2503` 的 safeChannel(s)；`internal/export/export.go:42-58`；`desktop/macos/RelayHubApp/main.m:109` 附近数据目录创建。

`ProxyURL` 允许常见 `socks5://user:password@host:port`，整体 TEXT 存储；safeChannel(s) 原样返回；不含秘密的配置导出仍包含完整 Channels。前端 Inspector 又显示 proxy_url。该 URL 字段在实际迁移中存在，与没有落库的 CustomHeaders 情况不同。

默认新 Go 数据目录是 0700、master.key 是 0600，属于已有保护；但已存在目录不会被 MkdirAll 收紧，macOS 壳/Docker `/data` 的目录创建也未显式统一为 0700。不能依赖文件权限解释 API/导出里的明文泄露。

**影响/前提**：配置带密码的代理；分享“无秘密导出包”、读取管理 API 或复制数据库时泄露代理凭据。

**建议/验收**：代理凭据拆入 SecretStore，只保存引用；返回/导出 URL 去 userinfo；迁移旧数据、清理历史包。对带 userinfo 的代理做 DB、API、日志、UI、include_secrets=false 的全链路断言，并实测目录/数据库权限。

### RH-32 · P2 · 长期运行缺少容量治理

**位置**：`internal/repository/repository.go:732-836` 的历史列表；`internal/api/server.go` usage/logs/checkin；`web/app.js:208-244,301`；`internal/affinity/sticky.go:35-54`；`internal/lab/capture.go:57-106`；scheduler/limiter 的 map。

历史表全量读取、没有 limit/cursor/保留清理，UI 每 15s 轮询；“最近 100 条渠道统计”也先读全部记录。Sticky 只在再次 Lookup 相同 session 时删除过期记录，唯一 session 不再访问就长期保留；scheduler/limiter 的渠道/key 状态也无统一清理。Lab 虽然按条数限制，但单条可接近 16MiB，关闭捕获不清空已保存 body；当前生产无法经 API 开启，属于 RH-05 修复后需要同步治理的风险。

**建议/验收**：数据库分页、SQL 聚合、保留/归档策略；TTL 扫描/LRU/最大 key 数；Lab 限总字节并支持清空，明确内存隐私语义。用长时与高基数输入测试 RSS、查询延迟和单连接 DB 对控制面的影响。

### RH-33 · P2 · 发布与验收流程有缺口

**位置**：`tests/macos/test_full_suite.sh:11`；`.github/workflows/*.yml`；`scripts/build-macos.sh`；`scripts/verify-release.sh`；`desktop/macos/LaunchAgent.plist`；`Makefile`。

- full-suite 脚本仍调用已不存在的 `docs/acceptance/packaging_probe.py`（V10）；不能再声称全套可运行。
- CI Go test job 不运行 `make check-web/test-web`；macOS release job 没有执行桌面生命周期测试；真实 Chromium 测试存在条件 skip，CI 没有明确 provision 浏览器。
- Docker job 仅本地 load/smoke，未配置 registry login/push、目标多架构 platforms；release 只上传 workflow artifact，没有创建 GitHub release。与 README 的“多架构镜像/发行”承诺不完整。
- Docker/macOS 构建仅 `-s -w`，没使用 Makefile 的版本/commit/date 注入；比较 version 的脚本还比较完整含 OS/arch 的字符串、对某些执行失败用 `|| true`，可能误报或漏报。
- LaunchAgent 指向 `Contents/MacOS/relayhub`，实际构建 Core 在 `Contents/Resources/relayhub-core`；备用 Swift 壳与实际 Objective-C 壳并存，易产生维护误判。
- 仓库声明 AGPL-3.0-or-later，但基线没有 LICENSE 文件；签名、公证、SBOM、镜像摘要固定等也未形成发布证据。此处为交付/合规待办，不作法律结论。

**建议/验收**：修正缺失路径，统一构建元数据，原生架构上严格运行实际产物；建立 frontend/Chromium/macOS/container 外部可达性门禁，发布与测试区分；确定平台支持与签名策略，补齐许可证文本。

## 5. 全模块覆盖矩阵

“静态”表示本轮审查范围，不表示该模块已通过 Go 测试或没有其他问题。测试文件数不等于覆盖率。

| 模块 | 非测试 Go / 测试文件 | 审查重点与发现 |
|---|---:|---|
| `cmd/relayhub` | 3 / 1 | 参数、地址限制、MCP、退出；RH-04/25 |
| `internal/adapter`（含 5 个内置适配器/testhelper） | 11 / 8 | 凭据、刷新、签到证据、HTTP client、声明式；RH-10/11/26 |
| `internal/affinity` | 1 / 1 | session 粘性、过期与容量；RH-32 |
| `internal/api` | 5 / 10 | 全部注册表、CRUD、browser boundary、同步、导入导出；RH-01/02/05/10/15/29/30/31 |
| `internal/app` | 2 / 4 | 生产依赖接线、默认监听、凭据、启停；RH-04/06/08/11/22/27 |
| `internal/audit` | 1 / 1 | SQL sink、元数据脱敏、调用覆盖；RH-30；保留该功能代码 |
| `internal/auth` | 1 / 2 | key 生成/哈希/验证/吊销；RH-30 |
| `internal/browser`（含 cdp） | 6 / 11 | 进程组、路径、下载、手动降级、WebSocket；RH-03/10/20/28 |
| `internal/buildinfo` | 1 / 1 | ldflags 默认值、产物版本；RH-33 |
| `internal/catalog` | 1 / 1 | 关系校验、Load/Snapshot、真实写入口；RH-02/08/15 |
| `internal/checkin` | 2 / 6 | 调度、去重、取消、manual、持久化；RH-10/15/22/32 |
| `internal/clisync`（3 个客户端） | 7 / 2 | 文件写入、密钥、校验、备份恢复；RH-23/24/25 |
| `internal/config` | 1 / 1 | Load/default/env 与 CLI 使用关系；RH-04；Load 未进入主运行入口 |
| `internal/domain` | 1 / 0 | JSON 字段、敏感字段、数据库对应；RH-09/11/16/31 |
| `internal/export` | 4 / 1 | Argon2id/GCM、checksum、依赖图、事务；RH-13/14/31 |
| `internal/gateway` | 6 / 6 | 请求/响应/流转换、认证、重试、用量；RH-06/07/10/11/12/17/18/19/20 |
| `internal/guard` | 1 / 1 | 重复请求检测和真实调用点；RH-06 |
| `internal/health` | 1 / 2 | 熔断、半开、恢复、指标输入；RH-18 |
| `internal/identity` | 1 / 0 | Proxy/UA/timezone 与实际出站路径；RH-09/10/31 |
| `internal/lab` | 1 / 1 | 捕获默认值、body 暴露与保留；RH-25/32 |
| `internal/logging` | 2 / 1 | 结构化与字符串脱敏、默认行为；补充观察见下 |
| `internal/mcp` | 1 / 1 | JSON-RPC 与 MCP 协议形状；RH-25 |
| `internal/proxy` | 3 / 2 | HTTP/CONNECT/SOCKS5、DNS target policy、隧道；RH-20/21 |
| `internal/ratelimit` | 1 / 1 | AIMD 令牌模型与调用 key；RH-19/32 |
| `internal/repository` | 2 / 2 | 所有资源读写、事务、SQL 参数化；RH-02/09/13/14/15/16/32 |
| `internal/router` | 2 / 2 | 匹配、候选、能力、fallback、策略、快照；RH-02/07/08/12/18 |
| `internal/secrets` | 2 / 1 | GCM/AAD、文件 key 权限、密文 backend；RH-11/14/27/31 |
| `internal/storage` | 2 / 3 | 8 个迁移、事务、WAL/FK、列兼容；RH-02/09/16 |
| `internal/usage` | 1 / 0 | 元数据 Recorder、失败传播；RH-17/32 |
| `internal/verify` | 1 / 1 | 模型身份/缓存/状态评分和输入；RH-17 |
| `internal/web` + `web` | 1 / 0 + 3 个 Node 测试 | embed、资源一致性、DOM sink/事件/API；RH-01/05/29 |
| `tests/` | 9 个 Go 测试文件（已计入全仓 83） | E2E、Docker、本地代理、usage、CDP；动态 Go 测试受阻 |
| `desktop/macos` | 非 Go | 实际/备用壳、Keychain、plist、资源布局；RH-27/33 |
| `.github`、`docker`、`scripts`、根配置 | 非 Go | 构建/发布/网络/权限/验收命令；RH-04/33 |
| `docs`、README | 非 Go | 安全与功能承诺、缺失/过时路径、旧审计清理；RH-25/27/28/33 |

### 已存在的有效防护

- 本地 key 使用随机 32 字节和 SHA-256 摘要保存，验证用 constant-time compare；未看到把 raw key 作为 local_api_keys 持久化的代码。
- SecretStore 用 AES-GCM，随机 nonce，AAD 绑定 secret ref，拒绝被挪到另一 ref 的密文。
- FileKeyProvider 对现有主密钥检查普通文件/权限/长度，新文件用 O_EXCL、0600；不是硬编码主密钥。
- SQL 主要通过占位参数绑定，动态列/表名来自代码常量；没有确认来自用户输入的 SQL 拼接注入路径。
- 管理面具备 Origin/Host/Fetch Metadata 防护；远程模式构造器要求 token、allowlist、rate limit。
- Proxy 解析并验证地址后拨选定 IP，降低检查后再次 DNS 解析造成的 rebinding 风险。
- 浏览器 profile 路径做 segment 哈希/包含性校验；不自动绕过人工验证；checksum 未配置时拒绝下载，而不是接受未验证二进制。
- checkin 记录保存失败有独立 persistence_failed 状态；CLI 初始文件写入采用临时文件+fsync+rename。

这些防护并不抵消真实接线和旁路中的问题。

### 补充观察（未计入 33 项）

- BaseNewAPIAdapter.RefreshAccessToken 的账号锁内读取的是调用方已加载的 `cred`，不是重新读取已轮换 secret；共享账号并发请求仍可能顺序消费同一旧 refresh cookie。SaveCredential 错误还被忽略；建议建立真实并发轮换测试。
- FetchStatusQuotaPerUnit 在 HTTP 非 200 时早退，未关闭已获得的 body；长时错误响应应验证连接资源释放。
- 日志脱敏正则不识别独立 `rh_` token；反射递归不处理 struct/pointer 内部字段。当前正常持久化只写摘要不等于所有未来日志路径安全，应以真实 secret fixtures 扩充测试。
- Model.Aliases 与 Archived 等字段在 resolver 中没有完整语义执行；批量 archive 通过同时 disable 部分缓解，但直接 CRUD 仍能产生不一致状态。
- 管理 CRUD 的 PATCH 多数实际是全对象替换，未提供字段 presence/merge 语义，客户端漏字段可能清空配置；应明确 API 契约。
- 非流 JSON `null` 可通过 JSON 合法性检查，但 response map 为 nil；同协议返回 null 而非协议错误，需增强响应 shape 校验。

## 6. 依赖与供应链

基线直接依赖：

| 模块 | 版本 | 本次结论 |
|---|---|---|
| `github.com/google/uuid` | v1.6.0 | 静态查看用途；未运行漏洞扫描 |
| `github.com/pelletier/go-toml/v2` | v2.2.3 | CLI 配置解析；未运行漏洞扫描 |
| `golang.org/x/crypto` | v0.36.0 | Argon2id；版本级漏洞不等于本项目调用可达 |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML 配置；未运行漏洞扫描 |
| `modernc.org/sqlite` | v1.39.1 | 纯 Go SQLite；Python SQL 验证不能代替此依赖运行验证 |

`go.sum` 存在。CI action 使用版本标签而非提交 SHA；Docker base image 使用 tag 而非 digest。后续应在可联网的受控构建环境生成 SBOM、运行 `govulncheck ./...` 并保存工具版本、数据库时间和可达性结论；逐项升级验证，不能仅根据“版本旧”认定某 CVE 可利用。

## 7. 可复核证据与后续测试

### 7.1 本轮只读探针

```bash
python3 scripts/audit-evidence.py
make check-web test-web
node --check web/app.js
node --check internal/web/app.js
for f in $(git ls-files '*.sh'); do bash -n "$f" || exit 1; done
```

脚本不联网、不写数据库文件、不使用凭据、不触发签到；它不是长期安全回归测试，`OBSERVED` 表示观察到基线问题，不是“测试通过”。修复后出现 `NOT_OBSERVED` 需要重新审查，而非自动安全认证。

本次输出摘要：

| 探针 | 结果 |
|---|---|
| V01 | rowHTML 的 ID 与 tag 输入分别产生 1 个 `img/onerror` 属性 |
| V02 | 所有 fetch 拒绝，仍调用 `core-live.classList.remove('off')` |
| V03 | 8 个迁移按顺序在内存 SQLite 执行成功 |
| V04 | channels 表没有 custom_headers |
| V05 | cache_read_tokens=81 已存储，但源码 SELECT 只取旧 12 列 |
| V06 | FK 启用情况下允许模型组指向自身；selector 未检查 visited |
| V07 | FK 启用情况下允许 provider=a 的映射绑定 provider=b 的 channel |
| V08 | managementRoutes 没有注册 extras |
| V09 | gateway 仅有 Guard 字段，没有 Trip 调用 |
| V10 | macOS full suite 指向不存在的 packaging_probe.py |
| V11 | Codex Syncer 设置 env_key，但从不读取 desired.APIKey |

### 7.2 具备工具链后必须补跑（本次未执行）

```bash
go version
test -z "$(gofmt -l cmd internal tests)"
go build ./cmd/relayhub
go vet ./...
go test ./... -count=1 -timeout=15m
go test -race ./... -count=1 -timeout=15m
go test ./... -coverprofile=coverage.out
# 在记录了版本的 govulncheck 工具可用后：
govulncheck ./...
./scripts/smoke-local.sh
```

另需独立运行：真实浏览器 XSS/离线/CRUD 测试，CDP 竞态与帧 fuzz，Docker **宿主外部**网络 smoke，macOS 原生两架构生命周期、MCP/三种 CLI 互操作，以及导出→空库导入→真实网关调用闭环。

注意：真实浏览器用例缺 Chromium 会 skip；要求输出测试数量和 skip 原因。macOS full_suite 的失效路径须先修复；不能因为脚本语法检查通过就当作运行通过。

## 8. 修复顺序与放行条件

### 阶段 A：先阻止安全/数据损失（P1）

1. RH-01：DOM 构造与输入/输出边界修复。
2. RH-02/03/08：关系校验、递归上限、CDP 同步和 resolver 原子发布。
3. RH-10/31：渠道统一出站与代理秘密引用。
4. RH-13/14/15：全配置包、密钥同事务、同步不丢绑定且更新快照。
5. RH-04/05/12/23：容器可达性、API 注册、协议支持矩阵和 CLI 密钥交付。

### 阶段 B：恢复可信运行（P2）

- 修复策略、guard、AIMD、熔断与终态记录；让监控反映实际失败和端到端延迟。
- 完成代理 EOF/缓冲语义、超时和生命周期排空。
- 明确占位能力，未完成的 MCP/回放/Tier2/Keychain 不应以“已支持”呈现。
- 修复 UI 操作与错误状态，补齐审计、回滚、容量保留策略和发布门禁。

### 最小发布门禁

- P1 清零或有书面范围缩减和验证过的风险缓解；不能只改文档掩盖仍在可用入口中的漏洞。
- build/vet/test/race/frontend 检查全通过，关键集成测试无不明 skip。
- 导出导入对完整拓扑做 round-trip，包含多密钥、模型组和失败回滚。
- 实际浏览器、实际容器网络、macOS 原生 shell 和目标 CLI/MCP 客户端通过互操作验收。
- 依赖扫描与产物 provenance/version/checksum 可追踪；安全文档与实际运行接线一致。

## 9. 旧审计报告清理与本轮变更

- 审计前工作树干净；对 Git 跟踪清单和工作树执行 audit/审计类名称及内容引用检索，**没有找到仍存在的独立旧审计报告**，因此实际删除旧报告数量为 **0**。
- `docs/dev/STATUS-2026-09-21.md` 明确记录过“删除过期 AUDIT”；它是历史进度快照，不是旧审计报告，本轮保留。
- `internal/audit/audit.go` / `audit_test.go` 是产品审计日志模块，**没有删除**。
- 旧计划对 `AUDIT-REPORT.md` 的引用原本悬空；本轮建立根目录唯一当前报告，并将计划引用改为明确链接。历史问题注释不当作报告文件批量清除。
- 新增 `scripts/audit-evidence.py` 供只读复核，README 添加报告入口。
- 未改业务代码、迁移、依赖版本或运行配置；未修改 Git 历史，未提交/推送。

---

**最终判断**：项目具备不少正确的安全基础和模块级测试，但存在明显的“模块实现与生产接线不一致”问题。应以本报告的真实入口验收清单而不是单模块测试数量或历史完成状态作为发布依据。
