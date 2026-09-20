# Gemini 三路修改：主助手独立验收

## 结论：部分通过，整体不通过

此结论基于当前工作区、主助手重新执行的命令及独立反例，不采信子代理自报。未提交或推送代码。

## 已独立验证

- `go test -race ./... -count=1 -timeout=5m`：退出码 0。
- `bash tests/macos/smoke.sh`：退出码 0；真实生成 apple-silicon/intel DMG，arm64/x86_64 架构正确，CFBundleExecutable 与实际文件名一致，本机 Intel 二进制 `-version` 能运行。ARM 产物未在 ARM 主机运行。
- 浏览器真实探测：Google Chrome 153.0.8010.47，路径 `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`。
- `docker version`：退出码 127，Docker 命令不存在。不能声称镜像构建、容器运行或容器健康状态通过。
- 健康检查脚本对本地临时 HTTP 服务的非默认端口退出 0；关闭端口后退出 1。仅证明脚本选址逻辑，不证明容器部署。

## 阻断项

### B1 浏览器进程回收不完整

`internal/browser/runtime.go:73-111` 只给直接子进程发信号，未回收后代进程；StartProcess 的 `cmd.Wait()` 与 Close 的 `Process.Wait()` 存在重复等待设计。

主助手探针 `go run ./docs/acceptance/browser_probe.go` 启动 shell + sleep 后代；`Close()` 返回 nil、跟踪数为 0，后代仍存活：

```
close_error: <nil>
tracked_after_close: 0
descendant_alive_after_close: true
```

探针按记录的 PID 清理自己的后代进程；不操作用户浏览器。

### B2 不同账号 profile 发生碰撞

`internal/browser/profile.go:18-44` 先 TrimSpace，且安全字符 ID 原样保留；因此 `account` 与 ` account ` 碰撞。另一个反例是 `a/b` 清洗所得目录名作为合法账号 ID 输入，两者仍得到同一路径。

独立探针结果：`sanitized_id_collision=true`、`trimmed_id_collision=true`。路径不越界不等于账号隔离成立。

### B3 签到运行时未闭环

当前只实现探测、路径函数和进程管理基础设施，没有实际 CDP 签到执行。`internal/api/server.go:1315-1337` 对签到 POST 仍返回 unsupported/501，查询列表仍声明 scheduler 未配置。`web/app.js:222,765` 两个签到按钮捕获并忽略错误，然后显示提示。无法验收用户的签到核心目标，更不能称 Tier 3 人工操作闭环已完成。

范围责任：上一轮主助手自行把委派范围缩成 Tier 1/3 基础设施，并明确排除了 Tier 2 下载和 CDP 驱动；这不是用户取消这些目标，也不能全部归咎于子代理。

### B4 macOS smoke 吞掉真实启动失败

`tests/macos/smoke.sh:91-96` 用 `|| true` 吞掉退出码，只检查输出非空。独立隔离反例使用输出 `FATAL startup failed` 并退出 42 的测试可执行文件，smoke 仍退出 0，并输出 Successfully launched。

复现：`python3 docs/acceptance/packaging_probe.py`，其中 smoke_negative_control 是明确的测试替身反例，不是实际 DMG 成功证据。

### B5 macOS 桌面完整启动未实现

`build-macos.sh:26` 仍只把 Go CLI 塞入 .app；Swift/ObjC 壳未参与构建。`cmd/relayhub/main.go:44-61` 的 full-stack 默认 false。

对真实构建出的 Intel app 主程序进行隔离 HOME、无参数启动，日志出现 started，但该 PID 没有 TCP 监听。此证据来自原生二进制默认启动，不是 Finder/UI 截图验收。只能认定版本命令与打包元数据通过，不能称桌面壳完成。前一轮主助手同样擅自缩窄了壳实现范围。

### B6 Docker 端口配置不是端到端配置

`RELAYHUB_MANAGEMENT_ADDR` 目前由 healthcheck 消费；Core 启动路径 `cmd/relayhub/main.go` -> `internal/app/app.go` 未把它写入监听配置。改变环境变量只会移动探针目标，不会移动 Core 监听。Dockerfile 没有 HEALTHCHECK 指令，只有 compose 定义了探针。普通 docker run 的 healthy 状态亦未得到验证。

## 验收边界

通过：已跑的 race 测试、打包产物命名/架构/执行文件名、浏览器探测、healthcheck 脚本本地端口覆盖。

未通过：账号隔离、完整进程回收、真实签到与人工兜底入口、桌面完整服务启动、smoke 失败判定。

环境阻塞：真实 Docker build/run。

留存：两个独立反例探针和本报告。当前真实构建产物保留在 dist/，未删除整个目录。未做真实站点签到、登录授权或用户 CLI 配置修改。
