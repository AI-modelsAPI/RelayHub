# RelayHub 桌面端范式（2026-09-20）

> 本文是 **唯一 UI 规范**。旧 Web Console（顶栏导航 + 表格 CRUD）已删除，不再作为参考。
> 视觉：科技、扁平、极简、亮色；主色 **深墨绿 `#143528 / #1B4332`**，辅色 **浅绿 `#52B788 / #D8F3DC`**，纸面 `#FBFCFB`。

---

## 1. 产品形态：一台机器上的「控制台应用」，不是网站

RelayHub 是常驻本机的基础设施（菜单栏 + 后台 Core），界面应对齐 **Xcode / Linear / 1Password / Little Snitch**，而不是 admin dashboard。

约束：

| 要 | 不要 |
|---|---|
| 单窗口、无浏览器 chrome | 多页网站、顶栏 logo + 一排 `<a>` |
| 图标轨 + 列表 + Inspector 三栏 | 卡片栅格、统计大数字墙 |
| 选中即检视，几乎无 Modal | 弹窗表单、全屏 wizard |
| 底栏状态（端口 / cache / trust） | 面包屑、分页器 |
| `⌘K` 命令面板 | 站内搜索框堆在每页顶部 |
| 12–13px 正文、大量留白、发丝分割线 | 阴影、渐变、玻璃拟态、插画空状态 |

交付形态分两层，**同一套 UI 资源**：

1. **现在**：`web/` 仍由 Go `embed` 挂在 `:8790`，但视觉与交互按桌面壳绘制（假红绿灯、图标轨、底栏）。便于无 macOS 时开发。
2. **目标**：Wails v2 把同一套 `web/` 装进无边框原生窗（交通灯用系统的；拖拽区已标 `data-tauri-drag-region`）。现有 `desktop/macos` 菜单栏只负责 **启停 Core / Keychain / Login Item**，不再 `open http://127.0.0.1:8790`。

Docker 镜像继续只跑 Core，不强制带 UI。

---

## 2. 窗口解剖

```
┌ titlebar:  ●●●    RELAYHUB  │  ⌘K 搜索     Core●  ┐
│ rail │  list (38%)          │  inspector (62%)     │
│  8   │  标题 + 过滤         │  无卡片，定义列表     │
│ icons│  行列表              │  分区标题大写字距     │
│      │                      │                      │
└ status: gw :8789 · cache 41% · trust 72 · 本地优先 ┘
```

- **Rail**（56px，深墨绿实底）：Pulse / Channels / Models / Identity / Check-in / Lab / Usage / Agents ；底：Settings。
- **List**：当前对象的可扫描名单。一行标题 + 一行元数据 + 右上角等宽标签。
- **Inspector**：只讲选中对象。禁止再套一层「面板卡片」。
- **Status bar**：等宽 11px，永远显示网关口、缓存命中、最低信任分。

键盘：`⌘K` 跳转；`⌘1…9` 切 rail（Wails 绑定）；`Esc` 关面板。

---

## 3. 页面 × 组件 × 后端（含复用）

每个页面只列 **真实组件** 与 **API / 包**。`*` 为需新增的后端。

### 3.1 Pulse「脉搏」— 开机默认

不是 overview 卡片。是 **此刻流量与风险**。

| 组件 | 数据 | 后端 |
|---|---|---|
| 英雄数字「今日缓存命中 / 省下 $」 | cache_read / prompt tokens | *`GET /usage/summary`；依赖 B：`RequestRecord` 加 cache 字段 |
| 实时 feed（时间 · 模型 · 渠道） | 最近 N 条 | 现有 `GET /api/v1/usage`，缺字段见缺口 6 |
| 拓扑条：Agent → Core → 上游 | 计数 | 现有 `GET /overview` |
| 「信任最低」名单 | Trust Score | *`internal/verify` + `GET /verify/scores` |
| 底栏 cache/trust | 同上 | 同上 |

复用：`usage.Recorder`、`overview` counts。

### 3.2 Channels

| 组件 | 数据 | 后端 |
|---|---|---|
| 过滤输入 + 行列表 | 名称、status、base_url | 现有 `GET/POST/PATCH /channels`、`/channels/batch` |
| Inspector：kv | provider、proxy、tags | 现有实体；`ProxyURL` 今日未接线（特性 C） |
| Inspector：密钥条 | 标签、disabled，永不回显明文 | 现有 `/channel-keys` |
| Inspector：测试 | 延迟 / 状态码 | 现有 `/channels/test` |
| Inspector：身份摘要 | bundle id | *C 落地后 |
| Inspector：信任条 | score + 三条证据 | *A |

删除：全宽 HTML table、批量工具条常驻（改为多选后底栏浮出「启用/停用/归档」，数量 < 4）。

### 3.3 Models

| 组件 | 后端 |
|---|---|
| 逻辑模型列表 | `GET /models`、`/models/catalog`、`/models/batch` |
| Inspector：能力（工具/视觉/推理/ctx） | `domain.Model` 已有字段；网关需在 `gateway.go:221` 填 router flags（缺口 2） |
| Inspector：ProviderModel 绑定 | `/provider-models` |
| Inspector：Route 命中预览 | `/routes` + *`POST /routes/explain`（H 的 explain 同源） |

### 3.4 Identity「身份束」— 新页面

把 C 做成一等公民，而不是渠道表单里的一个 URL 框。

| 组件 | 后端 |
|---|---|
| 每渠道一张 bundle 行 | *`IdentityBundle` 表或写进 Channel |
| Inspector：出口 IP 实探、UA、时区、CDP profile 路径、漂移告警 | *接线 `Channel.ProxyURL` 到 gateway / checkin / browser（今日零引用） |
| 「复制身份到…」 | 农场模板，M3 之后 |

### 3.5 Check-in

| 组件 | 后端 |
|---|---|
| 渠道签到状态列表 | 现有 `/checkin` |
| Inspector：最近 N 次、奖励、浏览器会话 | `checkin.JobState`、`browser.Executor` |
| 「立即全站」 | `POST /checkin`（已有） |

日历式时间线，不用表格。

### 3.6 Lab「实验室」— 新页面（F / A 主动探针 / D）

三段切换：回放 | 探针 | 对冲。

| 段 | 组件 | 后端 |
|---|---|---|
| 回放 | 捕获列表（默认关）、扇出 diff | *opt-in AES-GCM ring；复用 `secrets` |
| 探针 | 指纹 / 上下文针 / 假流式 | *`internal/verify`，调度复用 `checkin.Scheduler` |
| 对冲 | 路由上的 hedge 开关、预算 | *gateway errgroup；依赖流式解析器替换 `writeSSE` 全缓冲 |

### 3.7 Usage

| 组件 | 后端 |
|---|---|
| 两格：请求数、成功率（不要四面墙） | `/usage` |
| 行：模型 / 渠道 / 延迟 / cache | *扩展 `RequestRecord` |
| Inspector：`explain_last_request` | *H；现有 router.Decision 需落库 |

### 3.8 Agents

合并旧 CLI Sync + 未来 MCP，因为用户心智是「我的编程 Agent」。

| 组件 | 后端 |
|---|---|
| Claude Code / Codex / Hermes 三行 | 现有 `/cli-sync` Detect/Preview/Apply |
| Inspector：左右 diff（全页接管，不是弹窗） | 现有 preview；旧 `#view-diff` 的交互可移植，视觉重做 |
| MCP 工具清单 | *`internal/mcp` 薄封装现有 handler |
| 导入/导出 | 现有 `/import-export`，放 Settings 不放这里 |

### 3.9 Settings

系统偏好风格的定义列表：端口、语言、浏览器运行时、虚拟 Key 铸造（现有 `/keys`）、开机启动（桌面壳 API，非 HTTP）。

---

## 4. 视觉令牌

```
--deep:  #143528
--mid:   #1B4332
--sage:  #52B788
--sage-2:#D8F3DC
--bg:    #F3F6F3
--paper: #FBFCFB
--ink:   #0E1A12
--line:  #D5E0D6
```

无投影；圆角 ≤ 10px；分割线 1px；数字用轻微负字距。Rail 是唯一深色块。

---

## 5. 明确不做

- 暗色主题（本期）
- 移动端 / 响应式折叠 rail
- 图表库（Pulse 用数字与列表）
- 多窗口文档模型
- 再做一个独立 React/Vue 管理站
