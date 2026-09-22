# RelayHub 真机审计与修复报告 — 2026-09-22

- **审计基线**：GitHub `AI-modelsAPI/RelayHub` `main @ ea49fec`（PR #2、#3 合并后）
- **真机环境**：macOS 14.8.5 (x86_64) · Go 1.27.1 · Node 22.23.1 · Python 3.13.12 · Apple clang 16 · Google Chrome 已安装 · Docker：首轮无，同日补装 Colima + Docker CLI 后完成 §7
- **工作分支**：`fix/real-device-audit-2026-09-22`
- **前置文档**：`docs/audit/AUDIT-2026-09-22.md`（报告 A）、`docs/audit/AUDIT-REPORT-rh-01..33.md`（报告 B）、`docs/dev/STATUS-2026-09-22.md`（沙箱修复记录）。三份均**未修改**（STATUS 只加了一段后记）。
- **方法**：与前两份报告不同，本次全部结论来自真机执行：`go build/vet/test -race`、真实端口冒烟、真实 Chrome CDP、打包 `.app/.dmg` 并驱动、`govulncheck`、GitHub CI 日志。

---

## 0. 结论

1. **合并进 `main` 的"修复"根本没有编译过。** 沙箱无 Go 工具链，`STATUS-2026-09-22.md` 第四节"遗留强校验"从未执行；GitHub CI 在 `main` 上最近三次推送 **Tests & Lint 与 Docker 构建全部失败**（run 35681329624 / 35681329636：`internal/lab/capture.go:6:1: imports must appear before other declarations`、`internal/export/import.go:205: undefined: err`）。README 承诺的每一项能力在 `main` 上都是不可构建的。
2. 修掉 6 处编译错误后，真机 `go test -race` 仍有 **2 个失败**（一个是沙箱把测试和实现改成了互相矛盾的语义，一个是依赖公网 `1.1.1.1:443` 的网络假设）。
3. 真机测试脚本自身也是断的：`tests/macos/test_full_suite.sh` 调用不存在的 `docs/acceptance/packaging_probe.py`（RH-33 / V10）；`scripts/audit-evidence.py` 在 V05 探针处崩溃；`test_desktop_lifecycle.py` 硬编码 amd64 包（Apple Silicon 上测的是 Rosetta）；`verify-release.sh` 用陈旧的 `bin/relayhub` 做基准。
4. 修复后，真机上以下全部通过（证据见 §5）：`gofmt`/`vet`/`build`、**35 个包 `go test -race`**、`node` 8 条前端契约测试、`scripts/smoke-local.sh`（真实 8787–8790 端口）、真实 Chrome CDP 签到 e2e、**两架构打包 + DMG + verify-release + 桌面壳 4 个生命周期测试**、`govulncheck` 0 可达漏洞、`make test-real-device` 一键全套。

---

## 1. 真机首轮执行结果（修复前，`main @ ea49fec`）

| 检查 | 结果 |
|---|---|
| `gofmt -l cmd internal tests` | 5 个文件未格式化 + `capture.go` 语法错误 → **CI 第一步即失败** |
| `go build ./...` | **失败**：`internal/lab/capture.go`（`import` 在 `const` 之后）、`internal/export/import.go:205,329,330`（`err = repo.WithTx(...)` 但 `err` 从未声明） |
| 修上述两处后 `go build` | **仍失败**：`gateway.go:621 undefined: handler`（沙箱把 `*Handler` 写成 `*handler`）、`extras.go:143 s.Lab.Clear undefined`（调用了不存在的方法） |
| 修上述后 `go build` | **仍失败**：`gateway.go` `recordFailure` **重复定义**（沙箱把同一方法写了两遍，553 行与 621 行） |
| `go test -race ./...`（可编译后） | 2 失败：`TestP1_6_CLISyncLifecycleRedToGreen`（断言 preview 返回真实 key，而实现按 RH-23 改为占位符 `<<issued-on-apply>>`——测试与实现互相矛盾，两边都在同一个 PR 里）；`TestProxyDefaultAllowsPublicTarget`（对 `http://1.1.1.1:80` 发请求，收到 301 → 客户端跟随到 `https://` → CONNECT 443 在本网络超时；直连 `curl https://1.1.1.1` 同样超时，是网络而非代理问题） |
| `python3 scripts/audit-evidence.py` | **崩溃**于 V05（`re.search(...).group` 对 `None` 调用：仓库 SQL 已改为 `SELECT `+requestColumns+` ...` 拼接形式） |
| `tests/macos/test_full_suite.sh` | 第 2 步调用不存在的 `docs/acceptance/packaging_probe.py` |
| `tests/macos/smoke.sh` | 通过（两架构构建、DMG、lipo、amd64 启动） |
| `tests/macos/test_desktop_lifecycle.py` | 4/4 通过，但硬编码 `RelayHub-amd64.app`；`python -m unittest` 发现 0 个测试（函数式脚本，与 RH-33 记录一致） |
| `scripts/smoke-local.sh` | 通过 |
| GitHub CI `main` | Tests & Lint ✗ · Docker ✗（连续 3 次推送） |

## 2. 新发现（前两份报告未覆盖，全部由真机测试暴露）

| # | 问题 | 证据 | 处置 |
|---|---|---|---|
| RD-1 | **未知 `/api/` 路径返回 500 而非 404**：兜底处理器 `s.fail(w, r, errors.New(...))` 传的是裸 error，`encodeError` 对非 `fault` 类型一律编码为 `500 internal_error`。前端/MCP 对"路由不存在"看到的是"服务器坏了" | 新增 `TestManagementRouteTableIsComplete` 在真机首跑即失败：`unknown /api path: status=500` | 改为类型化 `notFound(unknownEndpointMessage)`；测试以该消息区分"路由缺失"与"处理器自身 404" |
| RD-2 | **HTTP 代理把"上游不可达"和"策略拒绝"都回 403**（CONNECT）或**静默断连**（普通 HTTP）。客户端无法区分被 RelayHub 拦截还是上游宕机；策略测试因此必须依赖公网 | `curl -x 127.0.0.1:8787 https://1.1.1.1` 超时 10s 后无状态码 | `rejectDial`：`errInvalidTarget` → 403，拨号/解析失败 → **502**；新增 `TestHTTPProxyUnreachableTargetIs502NotPolicyDenial` |
| RD-3 | **Codex 同步的 key 从未送达**（RH-23 沙箱只改了 service 层，`codex.go` 仍不读 `desired.APIKey`，V11 探针一直 OBSERVED）。Codex CLI 只从 `config.toml` 的 `env_key` 读变量名，变量本身由 `$CODEX_HOME/.env` 加载（Codex 源码 `load_dotenv()`） | `grep desired.APIKey internal/clisync/codex/codex.go` 为空 | 新增 `clisync.Engine.UpsertEnvVar`（原子、0600、就地替换、保留其他行），Codex/Hermes 共用；`Apply` 对空/占位 key **拒绝写任何文件**；`Verify` 检查 `.env` 中变量存在 |
| RD-4 | `lab.Ring` 截断请求体但不告知调用方；`Clear()` 不存在 | 编译错误 | 加 `Clear()`、`Truncated` 字段与 3 个单测 |
| RD-5 | `verify-release.sh` 以整行版本串比较（含构建时间与 os/arch），且优先使用已存在的陈旧 `bin/relayhub` | 真机首跑 `test_full_suite.sh` 第 2 步：`macOS app version (ea49fec-dirty) does not match binary version (0.0.0-dev)` | 只比较版本 token；无参数时**总是**重建基准；只执行与宿主 CPU 一致的包；校验 bundle 内 `relayhub-core` 存在 |
| RD-6 | `scripts/build-macos.sh`、`Dockerfile` 用裸 `-s -w`，打出的产物永远是 `0.0.0-dev (commit unknown)`（RH-33 提到但未修） | 首轮 smoke 输出 | 三处统一注入 `buildinfo.version/commit/date`；Docker 工作流传 build-args 并断言镜像不报 `0.0.0-dev` |
| RD-7 | `LaunchAgent.plist` 指向不存在的 `Contents/MacOS/relayhub`，且 `KeepAlive=true` 会让退出的菜单栏应用被 launchd 立刻拉起；`~` 在 `StandardOutPath` 不会展开 | 读 plist 与 build 脚本比对 | 指向壳 `Contents/MacOS/RelayHub`（由壳管理 Core 生命周期），`KeepAlive=false`，去掉无效日志路径，写明安装/卸载命令 |
| RD-8 | 前端"Core 在线"指示灯永远绿（V02 OBSERVED）；`#ch-new/#md-new/#lab-seg` 死按钮；Agents 页硬编码假数据；Settings 页静态文案；状态栏 `cache —/trust —` 写死；`#pulse-clock` 从不更新（报告 A §2.3 全部未修） | `audit-evidence.py` V02 OBSERVED；`grep '#ch-new' web/app.js` 为空 | 见 §3.4 |

## 3. 修改清单

### 3.1 让 `main` 可编译、CI 可绿（必须先做）
- `internal/lab/capture.go`：import 顺序；`Ring.Clear()`；`Capture.Truncated`
- `internal/export/import.go`：`err :=`
- `internal/gateway/gateway.go`：删除重复的 `recordFailure`（保留 553 行更完整的版本）
- `gofmt -w`：`extras.go`、`server.go`、`export/format.go`、`repository.go`、`router/selector.go`
- `internal/api/server.go`：未知 `/api/` 路径 → 类型化 404（RD-1）；`GET /api/v1/settings` 增加 `gateway_address`

### 3.2 让测试说真话
- `internal/api/p1_6_p1_7_test.go`：preview **不得**铸 key（断言占位符且 `localKeys.List()` 为空），apply 恰好铸 1 把
- `tests/e2e/proxy_policy_test.go`：改用本机 LAN 接口（`192.168.x.x`）上的 httptest 夹具验证 open 策略，禁止跟随重定向；无 LAN 接口时退回公网并只断言"不是 403"
- `internal/clisync/engine_test.go`：Codex 周期补"无 key 拒写 / key 不进 config.toml / `.env` 送达"
- 新增 `internal/api/route_table_test.go`：**路由表完整性**（33 条 method×path，任何一条落到兜底 404 即失败）、extras 方法契约（8 条 405）、`/api/v1/mcp` tools/list
- 新增 `tests/e2e/mcp_stdio_test.go`：**真实子进程** `relayhub mcp` 对着运行中的 Core 走 initialize → notifications → tools/list → 2 次 tools/call 的持久会话
- 新增 `internal/clisync/env_test.go`、`internal/proxy` 502 测试、`cmd/relayhub` 监听地址校验测试、`internal/app` `advertisedAddr/isLoopbackListen` 测试、`tests/docker` `TestDataPlaneListenAddrEnvOverrides`（真实二进制 + 环境变量，三个数据面监听全部搬家，网关 `0.0.0.0` 绑定后 settings 广告 `127.0.0.1`）
- 新增 `tests/web/api-contract.test.cjs`：**前端 `fetch` 路径 ⊆ `server.go` mux 注册表**（报告 A §7 的建议）、每个 `<button id>` 在 `app.js` 有处理器、不再宣传未实现的 lab 模式、离线态来自 overview 心跳
- `scripts/audit-evidence.py`：V05 解析拼接 SQL；V08 改查具体 extras 路径

### 3.3 真机测试脚本本身
- `tests/macos/test_full_suite.sh`：删除对不存在的 `packaging_probe.py` 的调用，改为 smoke → verify-release → `python3 -m unittest`
- `tests/macos/test_desktop_lifecycle.py`：改为 `unittest` 套件（`-m unittest` 现在发现 4 个测试）；按宿主 CPU 选包；生命周期测试增加"壳端口上 `/api/v1/usage/summary` 必须 200"（extras 在真实二进制上的回归）；统一 `stop()` 排空管道（消除 ResourceWarning）
- `scripts/verify-release.sh`、`scripts/build-macos.sh`：见 RD-5/RD-6
- `Makefile`：`test-real-device`（check-web + node + race + 真实端口冒烟 + macOS 套件）、`test-macos`、`vuln`；`format` 范围与 CI 对齐（加 `tests`）
- `.github/workflows/test.yml`：加 `setup-node` + `make check-web test-web` + `govulncheck`；`release.yml`：在 runner 原生架构上跑桌面生命周期套件；`docker.yml`：build-args 版本注入 + 镜像版本断言

### 3.4 前端（`web/` 与 `internal/web/` 已 `make sync-web`，byte-identical）
- 在线指示灯 = `GET /api/v1/overview` 是否成功；状态栏 `cache`/`trust`/`gw` 来自真实数据；`#pulse-clock` 显示最近刷新时间或"离线"
- 删除死按钮 `#ch-new/#md-new`（改为"API 管理"提示）与假的 回放/探针/对冲 分段；实验室改为真实的 **开始/停止捕获**（`POST /api/v1/lab/capture`）与 **清空**（`DELETE`），行显示截断标记
- Agents 页从 `GET /api/v1/cli-sync` 读取三种 CLI 的检测结果，点击行发 preview（只读、不铸 key）并展示 diff、变更键、历史记录
- Settings 页从 `GET /api/v1/settings` 读取网关/管理面地址、令牌状态、浏览器运行时
- 用量行点击 → `GET /api/v1/routes/explain?request_id=` 展示单次请求的路由解释；签到行点击展示奖励/错误/时间

### 3.5 容器可达性（RH-04，两份报告均列为 P1，沙箱未修）
- `cmd/relayhub`：新增 `-http-proxy-addr / -socks5-addr / -gateway-addr` 与 `RELAYHUB_HTTP_PROXY_ADDR / RELAYHUB_SOCKS5_ADDR / RELAYHUB_GATEWAY_ADDR`；`validateListenAddress` 在写任何状态前拒绝非法值；管理面**仍只允许回环**
- `internal/app/wiring.go`：非回环监听打 WARNING；广告给 CLI 同步/settings 的网关地址取**实际绑定端口**并把 `0.0.0.0`/`::` 改写为 `127.0.0.1`
- `docker-compose.yml`：默认 `network_mode: host`（bridge + `ports:` 到容器回环本就不通），注释给出 bridge 方案；`Dockerfile` 列出四个监听变量并说明代理未鉴权不得对外发布；`docs/deployment-docker.md`、`docker/README.md` 重写

### 3.6 文档对齐代码
- `docs/cli-sync.md`（preview/apply 语义、Codex `.env`）、`desktop/macos/README.md`（壳实际做什么、Keychain 未接线、真机套件）、`README.md`（测试章节）、`docs/dev/STATUS-2026-09-22.md`（后记）

## 4. 明确未做 / 仍需注意

| 项 | 原因 / 状态 |
|---|---|
| ~~Docker 镜像构建与容器冒烟~~ | **已补做**（同日，本机安装 Colima 0.10.3 + Docker 29.x 后），见 §7 |
| arm64 `.app` 实际启动 | 宿主为 x86_64，只做了 lipo 架构校验与 DMG 打包；amd64 包完整驱动通过。CI `macos-latest`（arm64）会跑另一半 |
| `release.yml` 里的桌面生命周期套件在 GitHub runner 上是否稳定 | 本机 GUI 会话下 4/4 通过；无头 runner 上 Cocoa 应用行为**未验证** |
| 真实 Claude Code / Codex / Hermes 二进制互操作 | 只验证到落盘文件形态（settings.json / config.toml + .env / config.yaml + .env）符合各 CLI 文档与源码，未启动这三个 CLI |
| Lab 回放 / 主动探针 / 对冲 | 仍未实现（`POST /api/v1/lab/replay` 返回 501）；UI 现在如实说明 |
| V07 跨 Provider 绑定 | 仅 SQLite 外键层面仍接受；管理 API 写路径已校验（`server.go:1829`）。未加数据库约束 |
| PR #1（`arena/01a0c0fa-relayhub`，OPEN） | 与本分支大面积重叠（`wiring.go`、`gateway.go`、`selector.go`、`server.go`…），合并顺序需人工决定，**必然冲突** |
| `actions/checkout@v4`、`setup-go@v5` 的 Node 20 弃用警告 | 未处理 |
| 前两份审计报告中的其余 P2/P3（多次反序列化、`config.Load` 无人调用、`mapRepoError` 字符串嗅探等） | 未在本次范围内 |

## 5. 证据（真机，修复后）

```
$ gofmt -l cmd internal tests                      # 空
$ go build ./... && go vet ./...                   # exit 0
$ go test -race ./... -count=1 -timeout=15m        # 35 个包 ok，0 FAIL，0 DATA RACE
    ok relayhub/cmd/relayhub  ok relayhub/internal/api (32.5s)  ok relayhub/internal/app
    ok relayhub/internal/clisync  ok relayhub/internal/proxy  ok relayhub/tests (真实 Chrome CDP e2e 3.9s)
    ok relayhub/tests/docker (真实二进制 + env 监听覆盖)  ok relayhub/tests/e2e (含 MCP stdio 子进程会话)
$ node --test tests/web/*.test.cjs                 # 8 pass / 0 fail
$ make check-web                                   # web assets in sync
$ ./scripts/smoke-local.sh                         # healthz → HTTP 代理 → SOCKS5 → 管理 API 建资源 → 铸 key → 网关 chat/completions 全部 OK
$ ./tests/macos/test_full_suite.sh
    ==> Build metadata: version=ea49fec-dirty commit=ea49fec date=2026-09-22T05:22:49Z
    [arm64] lipo: arm64 · DMG RelayHub-macOS-apple-silicon.dmg
    [amd64] lipo: x86_64 · DMG RelayHub-macOS-intel.dmg · launched: relayhub ea49fec-dirty (commit ea49fec, …)
    ==> Release verification passed successfully!   (macOS app version matched)
    test_core_crash ok · test_dual_instance_flock ok · test_full_lifecycle ok (Shell=8307, Core=8308) · test_unknown_port_fail_closed ok
    Ran 4 tests — OK
$ govulncheck ./...                                # 0 vulnerabilities affecting your code
$ python3 scripts/audit-evidence.py                # V01/V02/V04/V05/V06/V08/V09/V10/V11 NOT_OBSERVED；V03 PASS；V07 OBSERVED（DB 层，API 已校验）
$ make test-real-device                            # 一键全套，exit 0（见本报告提交时的 /tmp 日志）
```

手工代理探测（修复前，说明 RD-2 与网络环境）：
```
curl --noproxy '*' https://1.1.1.1/           → 超时（本网络屏蔽 1.1.1.1:443）
curl -x 127.0.0.1:8787 http://1.1.1.1/        → 301（普通 HTTP 经代理可达）
curl -x 127.0.0.1:8787 https://www.baidu.com/ → 200（CONNECT 隧道正常）
curl --socks5 127.0.0.1:8788 https://www.baidu.com/ → 200
```

## 7. 补充：本机 Docker 真机验证（同日追加）

环境：Homebrew 安装 `docker` 29.8.1 CLI + `colima` 0.10.3（vz 虚拟机，Ubuntu 24.04 x86_64，4 CPU / 6 GB）。

| 检查 | 结果 |
|---|---|
| `docker build`（默认 `GOPROXY`） | **失败**：容器内 `proxy.golang.org` 不可达（`dial tcp 142.250.77.209:443: i/o timeout`）。Dockerfile 新增 `ARG GOPROXY` 可覆盖；用 `goproxy.cn` 构建成功，镜像 64 MB |
| `docker run --rm relayhub:test -version` | 暴露新问题 **RD-9**：`entrypoint.sh` 的启动横幅写在 stdout，`verify-release.sh` 与 CI 的版本断言拿到的是"横幅+版本"两行。已改为 stderr；修复后 `verify-release.sh` 报 `Docker image version matched` |
| `tests/docker/smoke.sh relayhub:test` | 通过（UID 10001、容器内 healthcheck） |
| **bridge 模式**（`RELAYHUB_*_ADDR=0.0.0.0:…` + `-p 127.0.0.1:1878x:878x`） | 从 Mac 经发布端口：网关无 key 401 / 带 key 200；HTTP 代理与 SOCKS5 都能访问到**容器内回环独有**的 `127.0.0.1:8790/healthz`（Mac 本身 8790 无监听 → 证明流量真正穿过容器）；CONNECT 到公网 HTTPS 200；不可达目标 `127.0.0.1:1` 返回 **502**（RD-2 修复在真实容器上生效）；容器日志打出三条非回环 WARNING |
| **host 模式**（`docker compose up`） | 容器健康；套接字落在 Colima VM 的 `127.0.0.1:8787-8790`，Colima 自动转发到 Mac，`curl 127.0.0.1:8790/healthz` → 200、`8789/v1/models` → 401。注意：这依赖 Colima/Docker Desktop 的 host 网络转发，纯 Linux 上则直接是宿主回环 |
| `go test -race ./tests/docker/` | 2/2 通过 |

## 8. 补充：PR #1（`arena/01a0c0fa-relayhub`，09-22 06:35/06:51）合并记录

PR #1 是今天凌晨基于 09-21 归档快照做的 P0 功能线（健康数据求真 / 额度优先路由 / 统一出口 `internal/egress` / CDP 服务端证据 / 通知），作者在 Go 1.27.1 上真正跑过测试、其 CI 为绿。与真机修复后的 `main` 合并：**10 个文件 18 处冲突**，全部手工解决，原则是功能取 PR #1、审计修复取 `main`：

| 文件 | 取舍 |
|---|---|
| `cmd/relayhub/main.go` | 两者都留：监听地址校验（RH-04）+ `config.Load/ApplyEnv`（egress/notify） |
| `internal/app/wiring.go` | 两者都留：`MgmtHTTP` 优雅关闭 + `Notifier` |
| `internal/adapter/{base,generic,declarative}.go` | 客户端改走 PR #1 的 `HTTPClient/ClientProvider`（`clientForChannel` 已被 PR #1 删除）；保留 `identity.ApplyRequestHeaders`；generic/declarative 的回退逻辑对齐 base：注入的 selector → 渠道代理 → 适配器 client，不会静默回到默认出口（RH-10） |
| `internal/gateway/gateway.go` | 取 PR #1 的 egress 选择器与"每次尝试记一次健康结果"；删除 `main` 的 `upstreamClient` 缓存（被 egress 取代）；保留 `limitKey`（RH-19）与 `recordFailure` 用量/信任记账（RH-17）；**删掉沙箱在 transform/流结束后的第二次 `RecordSuccess`**——两段代码没有直接冲突却被 git 自动合并成双重计数，`TestGatewayRecordsMeasuredUpstreamLatency` 在真机上抓到（`WindowTotal:2`） |
| `internal/api/server.go` | 保留代理密码脱敏（RH-31）+ PR #1 的 `preserveSystemFields`；**新发现 RD-10**：管理探针（`fetch-models`、`channels/test`）仍用 `identity.HTTPClientE`，不认识 PR #1 的 `"direct"` 哨兵和全局出口默认值——渠道存 `proxy_url=direct` 后自己的连通性测试会 400。改为注入 wiring 共用的 `egress.Selector`；新增 `TestAdminProbesHonourDirectEgressSentinel` |
| `internal/browser/executor.go` | 取 PR #1 的 `launchArgs`（拒绝带凭据的代理，支持 direct）；删除 `main` 的 `normalizeCheckinProxy`（会重复追加 `--proxy-server`） |
| `internal/checkin/scheduler.go` | 取 PR #1 的 resolver + Selectors；无 resolver 时仍回退到渠道自身代理 |
| `.gitignore`、`.github/workflows/test.yml` | 并集（PR #1 的失败摘要注解保留） |

真机验���（合并树）：`go test -race` 38 包全过、node 8/8、`smoke-local.sh`、`make test-macos`（双架构 + verify-release + 4 生命周期）、Docker 镜像构建 + 容器冒烟 + bridge 模式（`RELAYHUB_EGRESS_PROXY=direct` 生效，settings 报 `egress.default=direct`）。

## 6. 建议的放行门禁（替代 STATUS 第四节）

`make test-real-device` 在一台有 Go 1.27 + Chrome 的 Mac 上退出码为 0，且 GitHub CI 三个工作流全绿。任何"已修复"的声明，若没有这两条证据，按本次经验应视为未修复。
