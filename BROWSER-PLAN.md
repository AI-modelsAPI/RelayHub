# RelayHub 签到浏览器运行时方案（BROWSER-PLAN）

**状态**：已与用户确认方向，待实现
**关联**：`AUDIT-REPORT.md` P1-9（五个签到适配器端点全是猜的）、autosign 源码（`/tmp/autosign`，AGPL-3.0，仓库 `AI-modelsAPI/autosign`）

---

## 1. 为什么必须有浏览器

读完 autosign 全部 11,012 行 Java 后确认：**签到不是纯 HTTP 能做完的事。**

`Catalog.java` 把五个站分成三种 `checkinType`，签到语义完全不同：

| 站点 | checkinType | 真实签到动作 | 需要浏览器？ |
|---|---|---|---|
| AgentRouter | `login` + needsRelogin | **登出 → 重新登录**，当日奖励只在发生新登录时由服务端发放 | 登录环节需要 |
| JustDoWork | `newapi` + needsRelogin | Turnstile 人机验证 + 重登 | **是** |
| GoRouter | `login` | 刷新会话，读当日奖励记录 | 否 |
| SeekAI | `newapi` | `POST /api/user/checkin`，但先过 Cloudflare Turnstile | **是** |
| KKtoken AI | `newapi` | 同上 | **是** |

`Engine.checkin()`（`Engine.java:964`）对 `newapi` 站直接返回 `needWebview:true`，纯 HTTP 路径明确放弃；实际签到走 `OffscreenCheckin` 的离屏 WebView，靠**真实浏览器环境**让 invisible Turnstile 无感通过。

autosign README 的红线（RelayHub 必须继承）：
> 优先使用站点公开业务接口，不解析额度页面、不伪造验证结果。需要 Turnstile 等验证时只使用正常 WebView/浏览器环境：无感验证可正常通过；要求交互时必须由用户完成，不绕过、破解或代答验证码。

GitHub OAuth 首次授权同理，必须可见浏览器。

---

## 2. 语言选型结论：与语言无关，不要引入新运行时

Turnstile 判定的是**浏览器真实性**（指纹、TLS 指纹、JS 执行环境、行为），不是驱动它的语言。

| 方案 | 实质 | 结论 |
|---|---|---|
| Playwright / Puppeteer（Node） | CDP → 真实 Chromium | 通过率同源，但引入 Node 运行时 |
| Selenium（Python/Java） | WebDriver → 真实浏览器 | 同上，更重 |
| **chromedp / rod（Go）** | CDP → 真实 Chromium | **通过率同源，零新运行时** |
| chromiumoxide（Rust） | CDP → 真实 Chromium | 同源，需引入 Rust |
| Swift/ObjC WKWebView | 系统组件，指纹最真实 | 仅 macOS，需 Go↔ObjC 桥接 |

设计规格 §3 与实施计划技术栈明确要求「单一可执行文件，避免 Node/Java/Python 多运行时依赖」——**因此选 Go 的 CDP 客户端（chromedp 或 rod）**。它们是纯 Go 的 CDP 客户端（几 MB），**驱动**浏览器而不打包浏览器。

真正决定通过率的三件事（与语言无关）：
1. 真实浏览器而非 HTTP 模拟；
2. **每账号独立持久化 profile**，让 cookie / localStorage / 指纹连续（对应 autosign 的 `WebViewProfileUtil`，它按「站点×账号」隔离 Profile）；
3. headless 模式：`--headless=new` + 持久 profile 通常可过 invisible Turnstile；更稳的做法是非 headless 但把窗口移出屏幕外。

---

## 3. 最终方案：三级回退，不预设机器上装了 Chrome

用户明确提出的约束：**换到别的机器上可能没装 Chrome。** 因此不能把「本机已装 Chrome」当作前提。

### Tier 1　复用系统已安装的 Chromium 内核浏览器（零下载）

按序探测，命中即用：

- macOS：`/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`、`Microsoft Edge`、`Brave Browser`、`Chromium`，外加 `~/Applications` 同名路径
- Linux：`google-chrome` / `google-chrome-stable` / `chromium` / `chromium-browser` / `microsoft-edge` （`exec.LookPath`）
- 环境变量覆盖优先：`RELAYHUB_BROWSER_PATH`

探测结果要能在设置页看到（路径 + 版本），不要只在日志里。

### Tier 2　自动下载 `chrome-headless-shell`（用户点头后）

Tier 1 全落空时，**不要直接失败**，而是在 UI 上给一个明确的一次性动作：「本机未检测到 Chromium 内核浏览器，下载轻量运行时（约 94 MB）以启用自动签到」。

实测体积（Google 官方 Chrome for Testing 分发，2026-09-14 查询 Stable 153.0.8010.36）：

| 产物 | 平台 | 体积 |
|---|---|---|
| chrome-headless-shell | mac-arm64 | **94.2 MB** |
| chrome-headless-shell | mac-x64 | 99.2 MB |
| chrome-headless-shell | linux64 | 114.2 MB |
| chrome（完整） | mac-arm64 | 182.2 MB |

下载要求：
- 版本清单走 `https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json`，**版本号写死在配置里可复现**，不要每次取 latest 导致行为漂移；
- 落盘到数据目录 `<data-dir>/runtime/chrome-headless-shell-<version>/`，**不写进安装目录、不污染系统**；
- 必须校验 SHA256 或至少校验 Content-Length + 解压后可执行；
- 下载失败要给出真实原因（网络/代理/磁盘），不要吞掉；
- 支持用户自带：设置页允许手填浏览器路径。

### Tier 3　人工兜底（永远可用）

用户拒绝下载 → 该渠道标记 `checkin_mode = manual`：
- 点「签到」用系统默认浏览器打开站点页面，用户自己点一下；
- **其余功能完全不受影响**：额度查询、刷新、Token 续期、登录型站签到、TOTP、加密存储、邀请额度划转都是纯 HTTP。

### 打包边界（关键）

- **安装包本身永不内嵌浏览器**：macOS `.app` 与默认 Docker 镜像体积不变；
- 94 MB 只在「机器上一个 Chromium 内核都没有」且「用户主动同意」时才落盘；
- Docker 出两个 tag：默认瘦身版（Tier 1/3），`:browser` 版预装 headless-shell（Tier 2 预置）。

---

## 4. 实现约束

1. **浏览器进程与 Core 生命周期解耦**：签到用完即关；Core 关闭时必须回收所有子进程，不留僵尸。
2. **每账号独立 `--user-data-dir`**：`<data-dir>/runtime/profiles/<provider>/<channel>/`，对应 autosign 的 Profile 隔离。禁止所有账号共用一个 profile。
3. **超时与重试**：单次签到硬超时（autosign 用 100s），Turnstile 明确 `wait-timeout` 只安全重试一次（autosign `OffscreenCheckin.runAttempt` 的做法），不无限重试。
4. **fail-closed**：拿不到浏览器、Turnstile 要求交互、身份不符 → 停止并说明原因，**绝不本地标记「已签」**。autosign 在 `Engine.java:1035` 有一条血泪注释：旧版在无任何签到信号时标已签，导致当日奖励永远领不到。
5. **不绕过验证**：不伪造 Turnstile token、不解析额度页面、不自动答题。
6. **日志脱敏**：沿用 `internal/logging` 的 Redactor，不记录 OAuth code/state、Token、Cookie、密码、TOTP。
7. **可关闭**：设置页允许彻底禁用浏览器签到，全站降级 Tier 3。

---

## 5. 从 autosign 移植的纯 HTTP 部分（不依赖浏览器，优先做）

这些是 RelayHub 现有五个适配器完全没有、但 autosign 已经验证过的真实逻辑：

| 能力 | autosign 出处 | 要点 |
|---|---|---|
| Refresh Cookie 续期 | `Engine.refreshAccessToken` | `POST /api/user/auth/refresh`；Cookie 会轮换，**每账号一把锁**，token+cookie 一次 patch 原子落库 |
| JWT 到期预判 | `SilentAuth.needsExchange` | 带 skew；非 JWT 交给 401 判定 |
| 额度折算 | `Engine.status` | `quota / quota_per_unit`，unit 从 `GET /api/status` 取，默认 500000 |
| 当日签到判据 | `Engine.checkinStatus` | `GET /api/user/checkin?month=YYYY-MM`，按**北京时间**比对 `checkin_date` |
| 奖励兜底 | `Engine.todayBonus` | 日志接口被 WAF 拦时的第二判据；金额在 content 文案里，`quota` 恒为 0 |
| 今日消耗 | `Engine.todayUsage` | `GET /api/data/self?start_timestamp=&end_timestamp=&default_time=hour` |
| 429 自动换代理 | `Engine.switchToBackupProxy` | 探测本机存活代理端口，60s 冷却防横跳 |
| 邀请额度划转 | `Engine.affTransfer` | `POST /api/user/aff_transfer`，站点最小 $1 |
| 凭据加密 | `Crypto` | AES-256-GCM；RelayHub 已有 `internal/secrets`，直接复用 |
| TOTP | `Crypto.totpOrLiteral` | RFC6238 SHA1 6 位；Base32 判定失败则按固定串处理 |
| 四态账号判据 | `Engine.acctState` | `ST_NEED_AUTH/ST_CHECKED/ST_PENDING/ST_WEB`，**全 UI 必须共用同一判据**，否则出现「顶栏说 3 待签，点一键签到说没有」 |

### 数据模型映射

**站点 = Provider，账号 = Channel。** 一站多账号天然对应 Provider 下多 Channel；账号持有的站点 API Key 同时作为路由出口凭据，签到与推理共用一套资源，不另起炉灶。

需要给 Channel 增补的字段（对应 autosign 的账号对象）：`site_cookie`、`site_user_id`、`github_account`、`last_checkin{date,reward}`、`checkin_mode(auto|manual)`。注意 `custom_headers` 目前在 domain 里有、数据库里没有（见 AUDIT-REPORT P3-7），一并补齐。

---

## 6. 验收标准

1. 一台**没装任何 Chromium 内核浏览器**的干净机器上：RelayHub 能启动、能查额度、能刷新、登录型站能签到；Turnstile 站显示「需人工」且点击能打开站点页面——**全程不崩、不假报成功**。
2. 同一台机器点「下载运行时」后：Turnstile 站自动签到可用。
3. 装了 Chrome 的机器：不触发任何下载，直接可用，设置页显示检测到的浏览器路径与版本。
4. Core 退出后 `ps` 里没有残留浏览器进程。
5. 任何情况下都不出现「本地标记已签但服务端无当日记录」。
