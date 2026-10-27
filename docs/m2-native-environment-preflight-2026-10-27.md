# M2 执行环境与出口检测 · 2026-10-27

本轮实现已确认的下一项：显式检查所选运行时、本地镜像、容器内工具和网络路径。**诊断不授权执行任务；原生任务执行器仍固定无网络，真实认证交接与在线编码尚未接通。**

## 入口与行为

“订阅账号”页底部新增“执行环境与出口检测”。填写工具、Docker/Podman、本地镜像、容器内工具路径、共享临时目录，以及可选网络目标。点击才执行；不自动启动引擎、拉镜像、登录、刷新认证或调用模型，也不修改账号网络绑定。修改表单会清除旧结果，晚到结果有代际保护，重复点击不会并发提交。

| 检查 | 实际含义 |
| --- | --- |
| 运行时 | 仅检查明确选择的引擎是否响应；不换引擎或宿主裸跑 |
| 本地镜像 | 本地 inspect 后锁定不可变 ID；仅支持 Linux amd64/arm64，run 固定 pull=never |
| 容器内工具 | 在断网、私有 HOME/配置目录中运行固定 `--version`；格式匹配只代表该命令通过，不证明来源、授权或模型权限 |
| 网络路径 | 目标留空则不请求；填写后由容器内内置 Go 程序发出一次 GET，不跟随重定向、不携带模型凭证 |
| 出口 IP | 仅显示目标返回的纯 IP 或 JSON `ip` 字段；其他正文不显示，不能代表所有域名与登录链路 |
| 指定代理 | 代理仅通过 helper stdin 交接，不放在启动参数、环境、磁盘日志或结果中；失败不回退直连、其他代理或宿主请求 |
| 强制出口 | 普通诊断没有验证 DNS/IPv6/UDP/浏览器旁路；Claude 在当前保护条件不足时不发送网络检测请求 |

容器联网路径属于所选运行时/虚拟机，不能仅凭宿主 TUN 配置就宣称容器出口一致。HTTP 仅允许显式本地地址（私有/回环 IP、localhost、两个内建容器宿主名），公网目标要求 HTTPS；目标拒绝认证、查询和片段。代理 URL 支持现有 HTTP/HTTPS/SOCKS5 解析与目标端解析规则。表单代理值只供本次使用，不保存到 localStorage/SQLite，检测结束清空。

## 实现与资源生命周期

- `internal/preflight/`：上下文期限、全局单个诊断、版本输出/原生日志限制、固定安全参数、独立清理及启动恢复。选定镜像只读根、普通 UID、移除 capabilities、no-new-privileges、资源限制、禁用镜像健康检查；不挂载项目、账号、Docker socket 或宿主 HOME。
- CLI 版本容器保持 `network=none`。只有固定内置网络 helper 使用普通 bridge；该 helper不运行镜像内的编程工具、不启动模型。显式清空继承的代理/Key 环境变量；Podman 还禁用自动代理注入。
- `internal/netcheck/` 与 `cmd/awb-network-probe/`：复用现有无代理回退的 HTTP 传输，只输出标准化连通性、IP 和错误码。原始错误、代理地址、响应正文不进入报告。
- `scripts/dev.py build/verify` 同时构建静态 Linux amd64/arm64 helper，桌面打包将它们放在后端旁。运行时缺少 helper 会显示未就绪，不在用户机器临时下载或编译。
- 每个诊断容器在启动前同步保存随机身份日志到后端数据目录 `environment-checks/`；日志只有引擎与独立夹具路径，没有请求 URL 或代理秘密。清理使用现有精确容器身份核验，Podman force 清理保留 `--time=0`。
- 清理不确认时保留日志/夹具并阻止继续检测。重启只清理已登记身份，不重发网络请求；不会按宽泛容器名前缀删除其他实例。

POST `/api/native-environment/check` 与受限 IPC `native.environment.check` 接通。仅人类身份，沿用 Origin/认证、严格 JSON 与大小限制，不接受任意命令、挂载、helper 路径或网络权限参数。`environment_ready` 仅为 CLI 与基本网络诊断通过，`execution_ready` 始终为 false。

## 已执行验证

- Go gofmt/vet/全量 race、构建、合同/协议/网络检查与 lint 通过；合并覆盖率约 81.2%。Docker/Podman 参数、工具缺失、失败/损坏响应、敏感值不进入启动参数/结果、未知输入、取消、并发、缺少 helper、清理日志保留/重启恢复及输出限制有自动测试。
- 24 项 UI、22 项桌面测试、TypeScript/生产构建通过。UI 覆盖显式触发、无登录/模型调用、重复点击、配置变化清除结果和晚到目录结果隔离。
- 真实 Podman、已有 Alpine 镜像、Linux arm64 helper：容器访问本地测试目标、带合成认证的代理、代理关闭后失败且直连目标计数不变、容器/夹具清理通过。Alpine 没有 Codex，正确报告 CLI 未就绪，未将网络通过误判为可编码。IP 为测试目标返回的文档地址，不是本机公网出口。
- M1 8 项、M2 7 项真实后端进程冒烟通过；官方 CLI 独立空账号协调探针通过，无登录/推理或明文认证文件。没有读取用户已有账号凭证。

macOS arm64 打包与启动/重启冒烟通过。实际桌面在独立 `output/ui-environment-qa-20261027/` 中验证了：本地目标返回合成 IP `203.0.113.9`、CLI 缺失与强制隔离未验证分别展示、深浅色保持结果、修改目标清除结果、留空目标重测只做离线检查。截图及 `result.json` 保存在该忽略目录；检测日志为空，无遗留诊断容器，QA 应用与本地目标服务已停止。

打包器默认 Electron 下载连接曾停滞，停止该次打包后，使用已安装的同版本 Electron 45.0.0 完成最终打包：`python3 scripts/frontend.py exec electron-builder --mac --arm64 --dir --config.electronDist=node_modules/electron/dist`。未改动系统网络或项目默认打包配置。

默认单元测试不启动容器；显式复现：

```sh
python3 scripts/dev.py build
AWB_TEST_SANDBOX_RUNTIME=podman \
AWB_TEST_SANDBOX_IMAGE=sha256:f28fb2fa6085390a967d9b74491b91c53cf00dec45bf1c7defa6aae11a52e519 \
AWB_TEST_SANDBOX_SCRATCH=/Volumes/D/Podman/workbench-probes \
go test -race ./internal/preflight -run '^TestRealContainerNetworkPreflight$' -v
```

测试使用两个本机随机端口；其他环境可通过 `AWB_TEST_PREFLIGHT_HOST` 指定容器可达的测试宿主名。没有访问公网服务或创建服务商请求。Docker 的真实运行、Linux amd64 实机及官方 Linux Codex 镜像尚未验收，发布包仍未签名/公证。

## 下一步

准备并核对包含官方 Linux CLI 的可追溯镜像、合法认证交接与固定网络绑定，再接通受控的原生试运行、预算和真实账号验收。Claude 的登录浏览器、续期、推理及网络层强制出口仍是独立未完成项；本次普通诊断不能解除保护要求。

运行参数核对依据：[Docker run](https://docs.docker.com/reference/cli/docker/container/run/)、[Podman run](https://docs.podman.io/en/stable/markdown/podman-run.1.html)。仅参考其能力与默认代理行为，实际验证范围以上述记录为准。
