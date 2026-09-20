# RelayHub Provider 适配器端点契约规范 (Adapter Contracts)

本文档是 RelayHub 五个签到适配器（AgentRouter、JustDoWork、GoRouter、SeekAI、KKtoken AI）的唯一规范依据，完全基于 `autosign` 实测源码（快照来源于 `AI-modelsAPI/autosign`，Android/Java 实现）提取。
依据 fail-closed 原则与实施计划 Task 9 要求：**凡无法从 autosign 源码实测确认的端点或能力，一律标记为 `unverified` 并保持在代码中禁用/显式拒绝，绝不猜测任何端点。禁止调用真实站点进行在线探测。**

---

## 1. 站点分类与总表 (基于 Catalog.java 与 Engine.java)

| 站点标识 (Key) | 站点名称 | 基准地址 (BaseURL) | checkinType | 登录重登特性 (needsRelogin) | 自动纯 HTTP 签到支持状态 | 状态说明 |
|---|---|---|---|---|---|---|
| `agentrouter` | AgentRouter | `https://agentrouter.org` | `login` (登录即得) | **true** (需登出后重登) | **受限/纯HTTP不可自动签** | 必须由浏览器执行登出+重新登录触发服务端额度发放。纯 HTTP 仅能查询状态与验证。 |
| `justdowork` | JustDoWork | `https://api.justwoker.icu` | `newapi` (New API 系) | **true** (需重登/CF Turnstile) | **纯HTTP禁用 (disabled)** | 启用 Cloudflare Turnstile 人机验证，纯 HTTP `CheckIn()` 必须返回 `needWebview: true`，拒绝假签。 |
| `gorouter` | GoRouter | `https://gorouter.app` | `login` (登录即得) | **false** | **支持纯HTTP查询验证** | 登录即得站，刷新会话/调用 self，以今日系统奖励记录为准。无独立 POST 签到接口。 |
| `seekai` | SeekAI | `https://seekai.cc` | `newapi` (New API 系) | **false** | **纯HTTP禁用 (disabled)** | 启用 Cloudflare Turnstile 人机验证，纯 HTTP 无法伪造验证响应，必须由浏览器环境处理。 |
| `kktoken` | KKtoken AI | `https://kktoken.cc` | `newapi` (New API 系) | **false** | **纯HTTP禁用 (disabled)** | 启用 Cloudflare Turnstile 人机验证，纯 HTTP 无法通过人机校验，必须由浏览器环境处理。 |

---

## 2. 认证与凭据管理 (Auth & Credentials Contract)

### 2.1 凭据解析原则 (Eliminating CredentialRef as Bearer Token)
- **硬错消除**：`channel.CredentialRef` 仅是 RelayHub 数据库/密钥存储中用于关联加密凭据的主键或引用 ID（例如 `"cred-xxx"` 或通道 UUID）。适配器**严禁**直接将 `CredentialRef` 字符串作为 `Bearer` token 发送给上游！
- **Secret Store 注入**：适配器必须通过 `SecretResolver` / `secrets.Store` 解密取出真实凭据。
- **凭据结构**：凭据明文可以是普通 API Key/Access Token，也可以是 JSON 格式的完整认证上下文：
  ```json
  {
    "token": "sk-...",
    "site_cookie": "session=...; new_api_refresh=...",
    "site_user_id": "1234"
  }
  ```
- 若无 `secrets.Store` 注入或找不到凭据，必须拒绝请求并返回错误，不能回退到明文 ref。

### 2.2 JWT 到期预判 (JWT Expiration Contract)
- 提取 JWT Payload 中的 `exp` 字段（秒级时间戳）。
- 判定条件：`now + SKEW_MS >= exp`（SKEW_MS 设为 60s，即提前 1 分钟判定过期）。
- 若 Token 并非有效 JWT（如无句点分割或解码失败），视为无法主动判断，交给业务接口的 401 响应触发续期。

### 2.3 Refresh Cookie 续期机制 (Token Refresh Contract)
- 触发条件：持有 `new_api_refresh` Cookie，且（Token 空 / JWT 即将到期 / 收到 HTTP 401）。
- 并发控制：**必须每账号一把互斥锁**，防止多个并发任务消费同一个一次性 Refresh Cookie。
- 请求端点：`POST /api/user/auth/refresh`
  - 请求头：
    - `Accept: application/json, text/plain, */*`
    - `Accept-Language: zh-CN,zh;q=0.9`
    - `Sec-Fetch-Dest: empty`
    - `Sec-Fetch-Mode: cors`
    - `Sec-Fetch-Site: same-origin`
    - `X-Requested-With: XMLHttpRequest`
    - `Cookie: <old_cookie>`
  - 响应处理：
    - HTTP 200 且 `success: true`，提取 `data.access_token`。
    - 提取响应中的 `Set-Cookie` 头，与旧 Cookie 合并轮换（删除态 `deleted` / 空值剔除，新值覆盖旧值）。
    - 轮换后的 Token 与 Cookie 必须原子写回凭据存储。

---

## 3. 公共端点契约 (New API 系端点)

### 3.1 站点元数据与折算率 (`GET /api/status`)
- 方法：`GET`
- 路径：`/api/status`
- 认证：无需凭据
- 响应结构：
  ```json
  {
    "success": true,
    "data": {
      "quota_per_unit": 500000,
      "turnstile_site_key": "0x4AAAAAA..."
    }
  }
  ```
- 规约：
  - `quota_per_unit`：系统额度对美元的换算率。若不存在或 `<= 0`，默认使用 `500000`。
  - 美元折算公式：`USD = quota / (double) quota_per_unit`（大于等于 1000 时保留两位小数）。

### 3.2 用户信息与基础额度 (`GET /api/user/self`)
- 方法：`GET`
- 路径：`/api/user/self`
- 认证：`Authorization: Bearer <token>` 或 `Cookie: <session_cookie>`
- 响应结构（New API 标准）：
  ```json
  {
    "success": true,
    "data": {
      "id": 1,
      "username": "user",
      "display_name": "User Display",
      "quota": 10000000,
      "used_quota": 500000,
      "aff_quota": 500000,
      "access_token": "fresh-token-if-present",
      "checked_in": false
    }
  }
  ```
  *变体说明*：AgentRouter 等部分站点将用户字段嵌套在 `data.user` 中，适配器需优先尝试 `data.user`，若无则回退到 `data`。
- 规约：
  - `quota`：当前可用额度原始值。
  - `used_quota`：已消耗额度原始值。
  - 严禁对 `needsRelogin` 站采信 `checked_in` 字段！

### 3.3 当月签到状态与记录 (`GET /api/user/checkin`)
- 方法：`GET`
- 路径：`/api/user/checkin?month=YYYY-MM`（注意 month 参数须为当前北京时间 UTC+8）
- 认证：`Authorization: Bearer <token>` 或 `Cookie: <session_cookie>`
- 响应结构：
  ```json
  {
    "success": true,
    "data": {
      "stats": {
        "checked_in_today": true,
        "records": [
          {
            "checkin_date": "2026-09-14",
            "quota_awarded": 10000000
          }
        ]
      }
    }
  }
  ```
  *变体字段兼容*：
  - `records` 可能在 `data.stats.records`，也可能在 `data.records`。
  - 签到日期字段可能是 `checkin_date` 或 `date`。
  - 获得奖励字段可能是 `quota_awarded` 或 `quota`。
- 判定逻辑：
  - 比对记录中是否存在与**北京时间 (Asia/Shanghai) 当天 `YYYY-MM-DD`** 完全一致的记录。
  - 若存在，`checked = true`，并解析出 `rewardUSD = quota / quota_per_unit`。

### 3.4 签到日志兜底查询 (`GET /api/log/self`)
- 方法：`GET`
- 路径：`/api/log/self?type=4&limit=30&page=1`
  - 关键规约：过滤签到日志必须传 `type=4`，不可传 `category=系统`（实测站点不认 category 参数）。
- 响应结构：
  ```json
  {
    "success": true,
    "data": {
      "items": [
        {
          "created_at": "2026-09-14 10:00:00",
          "type": 4,
          "content": "用户签到，获得额度 ＄20.00 额度",
          "quota": 0
        }
      ]
    }
  }
  ```
- 规约：
  - 日志列表是按时间倒序排列（最新在前），必须取第一条匹配项。
  - 必须通过 `isCheckinText()` 校验文案包含「签到/check-in/checkin」且不含「注册/邀请/兑换」。
  - 签到日志中的 `quota` 字段恒为 0，金额必须通过正则 `[＄$]\s*([0-9]+(?:\.[0-9]+)?)` 从 `content` 中提取。
  - 解析出的时间必须转换为北京时间并校验 `isToday(ms)`。

### 3.5 今日消耗查询 (`GET /api/data/self`)
- 方法：`GET`
- 路径：`/api/data/self?start_timestamp={start}&end_timestamp={end}&default_time=hour`
  - `start`: 北京时间当天 00:00:00 的秒级时间戳。
  - `end`: 当前秒级时间戳。
- 响应结构：
  ```json
  {
    "success": true,
    "data": {
      "data": [
        { "quota": 100000 },
        { "quota": 50000 }
      ]
    }
  }
  ```
- 规约：累加所有项的 `quota`，折算为 USD。

### 3.6 自动签到接口 (`POST /api/user/checkin`)
- 方法：`POST`
- 路径：`/api/user/checkin`
- 适用站点：仅限无 Turnstile 验证的标准 New API 站点（如开启了纯 HTTP 签到的兼容站）。
- 限制：
  - 对于启用了 Cloudflare Turnstile 的站点（JustDoWork、SeekAI、KKtoken AI），此纯 HTTP 端点直接调用会返回 400/403/WAF 阻断，或被站点要求人机验证。
  - 纯 HTTP 适配器在检测到站点为 `isAutoCheckin(site)`（即含有验证码）时，**禁止调用该接口假签**，必须明确返回 `needWebview: true`，错误码指示需要人机验证。

### 3.7 邀请额度划转 (`POST /api/user/aff_transfer`)
- 方法：`POST`
- 路径：`/api/user/aff_transfer`
- 适用站点：AgentRouter 等支持邀请额度划转的 New API 变体站点。
- 请求体：`{"quota": <aff_quota_long>}`
- 约束：划转最小金额为 $1 (`aff >= unit`)，不足 $1 时跳过。

---

## 4. 各站点独立契约与验证状态清单

### 4.1 AgentRouter (`agentrouter`)
- **官网**: `https://agentrouter.org`
- **checkinType**: `login` (登录即得)
- **needsRelogin**: `true`
- **已验证能力 (Verified)**:
  - `GET /api/status`: 支持，提取 `quota_per_unit`。
  - `GET /api/user/self`: 支持，用户字段位于 `data.user` 或 `data`；支持读取 `aff_quota`；支持捕获返回的 `access_token`。
  - `GET /api/user/checkin`: 支持，查历史记录。
  - `GET /api/log/self?type=4`: 支持，日志兜底。
  - `POST /api/user/aff_transfer`: 支持，邀请额度划转。
- **未验证 / 纯HTTP禁用项 (Unverified / Disabled)**:
  - `POST /api/user/checkin`: **Unverified & Disabled**。AgentRouter 是登录发放额度型站点，无独立 POST 签到接口。
  - `POST /api/user/auth/refresh`: **Unverified & Disabled**（实测返回 404，它是基于 session Cookie 的站，无 New API 标准 refresh 接口）。
  - 纯 HTTP 自动完成“重新登录发放当日额度”：**Disabled**。必须依赖后续由浏览器/WebView 执行登出并重新登录。
- **签到判据**:
  - 绝不采信 `self` 的 `checked_in` 字段！
  - 必须依靠 `checkinStatus`（北京时间当日记录）或 `todayBonus`（今日系统日志）。
  - 若今日无记录，必须返回 `Success: false, NeedsRelogin: true, Message: "站点无今日签到记录，需重新登录触发奖励发放"`。**严禁返回 Success: true**。

### 4.2 JustDoWork (`justdowork`)
- **官网**: `https://api.justwoker.icu`
- **checkinType**: `newapi` (New API 系)
- **needsRelogin**: `true` (需要重登或 Turnstile 离屏签到)
- **已验证能力 (Verified)**:
  - `GET /api/status`: 支持。
  - `GET /api/user/self`: 支持。
  - `GET /api/user/checkin`: 支持查询签到日历。
  - `POST /api/user/auth/refresh`: 支持 Refresh Cookie 轮换续期。
- **未验证 / 纯HTTP禁用项 (Unverified / Disabled)**:
  - 纯 HTTP `POST /api/user/checkin`: **Disabled (需人机验证)**。该站启用了 Turnstile 校验，纯 HTTP 请求无法通过人机验证。
- **适配器行为**:
  - `CheckIn()` 在纯 HTTP 下必须返回：`Success: false, NeedWebview: true, Message: "该站启用人机验证，需在后台签到窗口完成"`。

### 4.3 GoRouter (`gorouter`)
- **官网**: `https://gorouter.app`
- **checkinType**: `login` (登录即得)
- **needsRelogin**: `false`
- **已验证能力 (Verified)**:
  - `GET /api/status`: 支持。
  - `GET /api/user/self`: 支持，`checked_in` 布尔可用作兜底。
  - `GET /api/user/checkin`: 支持。
  - `GET /api/log/self?type=4`: 支持。
  - `POST /api/user/auth/refresh`: 支持 Refresh Cookie 续期。
- **未验证 / 纯HTTP禁用项 (Unverified / Disabled)**:
  - `POST /api/user/checkin`: **Unverified & Disabled**。GoRouter 额度随会话访问/登录发放，无 POST 签到接口。
- **签到判据**:
  - 先查 `checkinStatus` 与 `todayBonus`。
  - 若已存在今日记录，返回 `Success: true, Already: true`。
  - 若未检测到记录，可查 `self.checked_in`（非 relogin 站允许作为兜底）。
  - 若仍无任何签到信号，仅保活会话，返回 `Success: false, Message: "登录保活完成（未检测到今日签到记录，未标记已签）"`。**严禁假报成功**。

### 4.4 SeekAI (`seekai`)
- **官网**: `https://seekai.cc`
- **checkinType**: `newapi` (New API 系)
- **needsRelogin**: `false`
- **已验证能力 (Verified)**:
  - `GET /api/status`: 支持。
  - `GET /api/user/self`: 支持。
  - `GET /api/user/checkin`: 支持查询状态。
  - `POST /api/user/auth/refresh`: 支持 Refresh Cookie 续期。
- **未验证 / 纯HTTP禁用项 (Unverified / Disabled)**:
  - 纯 HTTP `POST /api/user/checkin`: **Disabled (需人机验证)**。该站强制无感/有感 Cloudflare Turnstile 人机验证，纯 HTTP 发送签到会被拦截。
- **适配器行为**:
  - `CheckIn()` 在纯 HTTP 下必须返回 `Success: false, NeedWebview: true`。

### 4.5 KKtoken AI (`kktoken`)
- **官网**: `https://kktoken.cc`
- **checkinType**: `newapi` (New API 系)
- **needsRelogin**: `false`
- **已验证能力 (Verified)**:
  - `GET /api/status`: 支持。
  - `GET /api/user/self`: 支持。
  - `GET /api/user/checkin`: 支持查询状态。
  - `POST /api/user/auth/refresh`: 支持 Refresh Cookie 续期。
- **未验证 / 纯HTTP禁用项 (Unverified / Disabled)**:
  - 纯 HTTP `POST /api/user/checkin`: **Disabled (需人机验证)**。该站强制 Turnstile 人机验证。
- **适配器行为**:
  - `CheckIn()` 在纯 HTTP 下必须返回 `Success: false, NeedWebview: true`。

---

## 5. 账号状态四态判据 (Account State Contract)

全系统（UI 摘要、调度队列、适配器结果）严格共用以下四态：
- `ST_NEED_AUTH` (0): 未授权。Token 为空且 Cookie 为空。
- `ST_CHECKED` (1): 今日已签。本地已有今日签到时间戳/记录。
- `ST_PENDING` (2): 已授权且今日未签。可作为签到目标。
- `ST_WEB` (3): 网页手动站（`checkinType == "web"`，RelayHub 后台不可自动签）。

---

## 6. 错误分类与映射 (Error Handling Contract)

| HTTP 状态码 | 业务错误分类 | 处理动作 |
|---|---|---|
| `200` | 成功 | 解析业务数据 |
| `401` | 认证失效 (Unauthorized) | 检查是否存在 `new_api_refresh` Cookie。若有，锁内尝试一次续期后重试；若续期失败或无 refresh cookie，报错凭据已失效，不得假装成功。 |
| `403` | 拒绝访问 / WAF 拦截 | 明确报告 403 / 访问受限，绝不误判为登录失效。 |
| `429` | 请求过频 (Rate Limit) | 标记 RateLimit 状态，提示换代理或退避，严禁重试风暴。 |
| `500/502/503/504` | 上游服务端错误 | 503 时退避 2.5s 可重试一次（针对 WAF 间歇拦截）；其他返回上游错误信息。 |
| 无法通过人机验证 | 需要浏览器交互 | 明确返回 `NeedWebview: true`，禁止伪造响应。 |
