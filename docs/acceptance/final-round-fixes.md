# 中断恢复后本轮主助手直接修复汇总

## 新增（plan 收尾）

8. Tier 2 运行时下载（BROWSER-PLAN §3）：新增 internal/browser/runtime_download.go。固定版本 153.0.8010.36；从 Chrome for Testing 形态的源下载 chrome-headless-shell 压缩包，SHA-256 校验失败即拒绝且不留残件；仅解出单一二进制（原子写 .part→rename，0755），落盘 <data-dir>/runtime/chrome-headless-shell-<ver>/，不碰安装目录；DownloadedRuntime 探测已装运行时；DownloadRuntime 幂等。测试覆盖：错误校验和拒绝+无残留、正确安装+版本探测、幂等、空数据目录报不可用、路径按数据目录隔离。平台校验和表待接入真实 CDN 哈希后填充，当前任何下载都会因"无固定校验和"拒绝——fail-closed，不安装未校验字节。

## 已完成（全部先 RED 复现再 GREEN，均为本机实测输出）

1. 管理边界 IPv6 误伤：Host: [::1]（省略默认端口）此前 403。新增反例并修复 browser_boundary.go 括号剥离；6 项边界用例（含 IPv6 同源两形态）全绿。
2. CDP 点击次序：轮询探针原内联点击每次都会再点一次，实测 7 次点击。改为轮询只读 + ready 后仅一次点击；服务端 /click 计数断言 =1。
3. 人机验证门（关键 fail-closed）：夹具缺失 charset=utf-8 时 Chrome 按 GBK 解码中文导致文本门失效，曾自动点击并报成功。修正后结构门（cf-turnstile/未解决 token）先于点击判定，未解决即 ErrCDPNeedManual，点击计数断言 =0。一次性诊断脚本已删除。
4. 隐藏成功元素不再被当作签到成功（hidden/visibility 检查），以超时错误收尾。
5. 持久化失败语义：新增 StatusPersistenceFailed + ExecutionStatus，保留原执行结果（success/need_manual），不再覆盖为 failed；API 与前端提示"结果保存失败，勿直接重试"。
6. 应用接线：internal/app 真实组装 CDPExecutor（共享 BrowserRuntime），新增端到端测试证明管理 API 触发的真实浏览器签到成功并落库。
7. 桌面壳（子代理，主助手独立复跑验证）：flock 排他、未知端口占用 fail-closed 不加载页面、PID+启动时间双校验防误杀、dispatch signal source 信号安全、--management-addr 透传；packaging_probe.py 动态端口+严格断言。
   复跑：packaging_probe.py 退出 0（全部 true），tests/macos/test_full_suite.sh 退出 0。

## 验证命令与结果

- go test -race ./... -count=1：全绿（含真实 Chrome 用例，无跳过）。
- go vet ./...、make build、make check-web、node --check web/app.js：通过。
- 上述桌面探针与套件独立复跑：通过。

## 仍未验收（不宣称完成）

- Docker 镜像构建与容器内真实运行：本机物理无 Docker/colima/podman/OrbStack（2026-09-18 复查确认），容器级验收保留缺口；已有 tests/docker 配置回归与 healthcheck.sh 真实探针覆盖。
- ARM64 实机运行（仅 lipo 静态校验）。
- 真实五个站点的线上自动签到与真实 Turnstile 通过（当前仅本地夹具 + 结构级验证）。
- Tier 2 运行时下载、GUI 截图证据。
- 未 commit/push。
