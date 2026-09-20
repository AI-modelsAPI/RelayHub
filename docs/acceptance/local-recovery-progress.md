# Connection error 中断后的本地接管

三路 deleg_7bb29b46 实际中断原因是 `API call failed after 3 retries: Connection error`，output_schema 只是外层失败信息。保留子代理已经落盘的更改，不重置仓库。

## 本轮实际修复与复验

- 浏览器原验收探针：descendant_alive_after_close=false、sanitized_id_collision=false、trimmed_id_collision=false。额外发现子代理仍会忘记已退出组长留下的后代；新增 runtime_leader_test.go，真实复现 RED 后修复，browser race 测试通过。
- Core 管理地址：新增 management-addr flag > RELAYHUB_MANAGEMENT_ADDR > 默认值；验证限制为 loopback 与有效端口。tests/docker 原有 RED 从不监听自定义端口变为通过；该测试实际编译并启动 Core。Dockerfile 加 HEALTHCHECK，探针增加连接/总超时与 curl 错误传播。容器构建/运行仍没有验证。
- 签到：Scheduler 注入管理API；GET返回实际记录；POST执行启用渠道并返回逐渠道结果。测试注入真实调度器及SQLite，人工模式返回 need_manual + manual_url，数据库记录 need_manual，不再501。没有执行器但探测到Chrome的场景从无效重试改为人工回退。这不代表CDP自动签到完成。
- 前端两个签到按钮不再吞掉HTTP错误，显示逐项结果和经http/https限制的人工链接；记录显示need_manual。已跑JS语法检查，未做真实界面截图验收。
- macOS smoke：失败输出非空且退出42的负对照此前退出0，现在退出1，accepted_fatal_output=false。原生壳子代理更改已落盘，但其完整生命周期、安全性与UI尚未验收，不能发布。

## 最新门禁

`go test -race ./... -count=1 -timeout=90s` 完整输出所有包通过；`go vet ./...` 无错误；`make check-web` 输出 web assets in sync；`node --check web/app.js` 通过。

## 仍待完成

- 独立只读控制面与生命周期审查已下发；其结果尚未回来，当前不可视为安全审查通过。
- 原生桌面壳的Core持有/退出回收、端口冲突、错误显示、LaunchServices启动及真实截图。
- CDP自动签到、Tier2下载与五站真实服务端奖励证据。
- Docker真实容器构建运行，以及容器loopback发布可达性设计。
- 更完整的签到批量并发、禁用provider、失败日志/持久化错误传播验收。

未提交或推送，未修改用户真实CLI凭据，未执行真实站点签到。构建产物尚不能作为已验收版本交付。
