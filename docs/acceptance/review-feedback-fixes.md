# 独立审查反馈复核与修复

## 采纳并复现

1. 跨站保护缺失成立：真实 httptest HTTP server 接收恶意 Origin、Origin:null、Sec-Fetch-Site:cross-site、DNS重绑定式Host时，创建provider请求均返回200。新增 browser_boundary_test.go，先RED后GREEN。managementRoutes统一包装边界：真实本地连接的Host限localhost/loopback；Origin须同源；Fetch Metadata拒绝跨站。无Origin的原生客户端与同源请求仍通过。未强行引入用户凭据或改用户CLI配置。
2. 停用provider仍执行成立，但审查描述不准确：Registry.Resolve并不会因provider.Enabled=false报错，实际是执行了适配器。计数测试复现disabled provider被调用1次。调度器新增channel/provider/checkin启用检查，执行前拒绝。
3. 记录写失败成立：成功路径返回nil，人工路径仅返回ErrNeedManual，均丢失存储错误。新增注入存储错误的auto/manual回归；统一传播wrapped error并将状态改为failed，避免宣称持久化成功。

## 不采纳的事实错误

审查声称 scripts/build-macos.sh 未编译原生壳是过时结论。当前第34-42行真实调用clang编译main.m，Go Core位于Resources/relayhub-core。已有打包不等于壳生命周期通过；PID所有权、端口冲突盲连等仍须处理。

批量签到、CDP驱动、Tier2下载和原生App完整交互仍未完成。内网manualURL是用户配置站点后主动点击的入口，不能仅因目标是内网就断言漏洞；当前已限制http/https并禁止userinfo，保留此功能。

## 验证

修复后主助手执行：
- go test -race ./... -count=1 -timeout=90s：全部通过、退出码0。
- go vet ./...：通过。
- make check-web：web assets in sync。

这些测试不构成五站真实自动签到、真实Docker部署或macOS GUI验收证据。安全补丁还须最新版本独立复审。未commit/push。
