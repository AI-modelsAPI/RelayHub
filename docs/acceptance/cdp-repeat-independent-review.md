# CDP 连续执行独立复验

## 已复现并修复

子代理任务完成状态与报告不能直接作为验收结论。主助手增加同一个 executor/runtime 连续两次成功签到的真实 Chrome 回归，首次执行：

```
--- FAIL: TestCDPExecutor_RealChrome/success_flow
second execution must succeed with same runtime: result={Success:false Reward: Message:} err=failed to start browser process: browser runtime is closed
```

根因：ExecuteCheckin 的 defer 调用了共享 Runtime.Close，第一次执行永久关闭整个运行时，并可能影响其他账号任务。

修复：新增 Runtime.StopProcess，仅回收本次拥有的 command 进程组，保留共享 runtime 可用；清理最多等待5秒，错误向调用方传播，不吞错。

同时将两处 fixture 外部 Cloudflare iframe 改为本地 srcdoc（先前“全部仅本地”描述不严谨）。E2E 新增 gateVisits 断言，确保第二条人工回退确实访问了本地门页面，而非仅因浏览器启动失败被统一降级。

## 主助手执行结果

- 连续两次签到、模拟门、超时真实 Chrome 测试：PASS。
- go test -race ./tests -run TestCDPExecutor_EndToEndFlow：PASS。
- go test -race ./... -count=1 -timeout=120s：全量PASS，包括真实Chrome用例，无Chrome跳过记录。
- go vet ./...：退出0。
- make build：退出0，产物 bin/relayhub。
- make check-web：web assets in sync。

## 未验收/仍需处理

- internal/app 中尚未调用 SetBrowserExecutor；真实产品运行路径仍未接上此执行器。这里只验收本地夹具的库与调度器测试。
- executor 的页面轮询仍包含点击，可能重复触发；固定通用选择器和DOM存在性不是实际站点业务成功的充分证据。不得直接启用真实站点自动签到。
- 桌面报告过度承诺：verifyProcessIdentity 只比较文件名，不足以“彻底杜绝”PID复用；端口占用仍直接进入观察者且加载该端口页面；token存在性检查不是原子互斥锁。
- packaging_probe.py 只输出布尔值，没有汇总失败退出断言；从全局进程列表取首个relayhub-core也不能证明是自己的Core。该脚本退出0不能证明全部验收通过。本轮没有运行此不可靠探针或宣称GUI验收通过。
- 独立审查给出的IPv6无端口Host误伤、持久化错误覆盖执行结果问题仍待独立RED→GREEN处理；它们不是本轮已修复项。
- Tier2下载、Docker实际部署、ARM实机、真实站点以及原生GUI截图验收仍缺。

未commit/push；本轮没有新增后台委派。
