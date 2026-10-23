# M2 原生账号运行协调层 · 2026-10-24

完成下一步的账号占用、认证所有权与退出撤销接线。依据 DESIGN 6.4：登录型 CLI 默认并发 1，刷新链由单一管理者持有，旧执行确认停止后才能交接认证。**尚未交接真实凭证或启用在线编码，M2 整体仍未验收完成。**

## 实现

- `internal/accountcoord/` 管理工作台专用 Codex 认证命名空间的一个保守共享槽。多个模型、角色或连接别名不能通过不同标签增加槽位；原生运行必须使用固定的账号资源引用。资源名 `codex-managed` 只代表该受管命名空间，`identity_scope=managed_namespace`、`quota_known=false`；没有凭空推断官方 issuer/account/workspace 或跨工作台的额度归属。
- 授权检查与刷新保持同一代际；登录切换、退出及后端重启使旧代际失效。执行引用无令牌，写入原生会话固定绑定。缺失协调器、旧引用、未授权、认证占用或清理未确认时拒绝取得槽位。
- `nativeservice.Run` 对显式绑定账号的内部调用取得执行租约，执行 context 随退出/切换/平台关闭而取消。租约持续到容器清理、草稿检查点和终态持久化完成；仅发出取消不会释放。清理/持久化无法确认会阻塞后续交接。SQLite 中已结束但清理未确认的账号运行，在新管理器启动时仍会阻塞该资源。
- 退出/切换先撤销代际、取消旧执行，并等待其收尾，再允许桌面触碰认证。检测、刷新和取消登录不会为取得认证目录而中断一个活动执行，冲突返回 409。多个认证动作不能同时取得许可，完成回执必须匹配当前操作。
- Electron 原有队列接入 Go 的 begin/finish 协议；官方 app-server 仍管理实际授权与刷新。浏览器授权未完成时保留认证所有权；其他操作在确认官方进程退出后才报告可交接。新增内部 `refresh()` 使用官方 `account/read` 的 `refreshToken=true`，不自动轮询刷新，没有渲染层刷新或任意 RPC 入口。
- 官方进程启动前以 `wx`、0600、flush 写入 `.workbench-auth-owner` 独占标记，内容只有随机编号。只有本实例确认进程退出或确认未启动后才能清除，多个收尾调用共享同一个 Promise。残留标记不按 PID 猜测或自动删除，新的实例会拒绝接手认证目录。
- 订阅页新增“账号占用”状态与计数，保持“账号授权”和“编码执行”独立展示；退出确认说明先停止关联执行再注销。没有开放原生启动/续接按钮。

## 接口与边界

仅本地人类身份可访问：

| 接口 | 作用 |
| --- | --- |
| GET `/api/native-account` | 读取协调状态，不启动 CLI |
| POST `/api/native-account/auth/begin` | `{kind}` 领取认证操作；kind 为 check/refresh/login/cancel/logout |
| POST `/api/native-account/auth/finish` | `{operation,authenticated,owner_active,healthy}` 完成当前操作 |

上述接口不接收令牌、认证文件、目录、原生 RPC 或容器配置。操作回执由桌面主进程持有，未增加对应的渲染层 IPC。沿用认证、Origin、严格 JSON 字段检查，Agent 身份无权调用。认证许可不是可执行模型的公开授权端点。

未绑定账号的旧离线合成运行保持原行为，仍固定无网络、没有凭证挂载；不是绕过账号槽的在线路径。数据库继续为 schema 5，新增可选绑定字段不改写旧记录。

## 验证

| 检查 | 结果与证据范围 |
| --- | --- |
| Go 并发/撤销 | 32 个竞争者只有一个取得槽位；检测/刷新不抢占；退出等待清理；旧代际、错回执、未授权、关闭和清理失败拒绝交接 |
| 原生服务与存储 | 退出后草稿/中断记录持久化才交接；旧引用不续接；清理债务跨管理器重启保留 |
| HTTP 边界 | Agent 全部拒绝，秘密/未知字段拒绝，认证并发与错回执为 409 |
| 桌面模拟 | 授权、串行刷新、退出、官方进程停止后发布状态、目录独占、并发收尾、信号退出及停止未确认保留标记通过；22 项桌面测试通过 |
| 实际 Podman | 已有 Alpine 不可变镜像内执行合成 CLI，保存草稿后退出账号，验证取消→容器清理→持久中断→释放槽位；调用计数仍为 1，旧引用拒绝续接。认证本身为模拟，不挂载凭证 |
| 实际后端 + 官方 CLI | 新建空认证命名空间，执行 initialize/account/read，确认官方进程退出后再完成认证协调；无明文 auth.json，未登录、未推理，槽位为 unverified、0/1 |
| 打包应用实际操作 | 独立 QA 目录中打开订阅页，显示“等待授权核对 · 0/1”；检测后未授权、编码未就绪，独占标记已清除，Claude 登录继续禁用 |
| 回归 | gofmt/vet/全量 race/编译/合同/协议/网络检查、lint 通过，合并覆盖率约 81.0%；22 UI 测试、TypeScript/构建、macOS arm64 打包与启动/重启、M1 8 项和 M2 7 项进程冒烟通过 |

实际页面截图及摘要位于忽略目录 `output/ui-account-coordination-qa-20261024/`，QA 应用已关闭。官方空账号报告为 `output/verification/native-account-coordination.json`。没有读取用户既有登录文件或请求模型；真实容器中的脚本不等于真实 Codex 编码。Docker 本轮未做真实运行验证，发布包未签名/公证。

复现空账号链路（先构建后端）：

```sh
python3 scripts/dev.py build
python3 scripts/frontend.py exec node scripts/native-account-smoke.cjs
```

显式真实容器退出验证（替换为自己已有镜像与共享目录）：

```sh
AWB_TEST_SANDBOX_RUNTIME=podman \
AWB_TEST_SANDBOX_IMAGE=sha256:f28fb2fa6085390a967d9b74491b91c53cf00dec45bf1c7defa6aae11a52e519 \
AWB_TEST_SANDBOX_SCRATCH=/Volumes/D/Podman/workbench-probes \
go test -race ./internal/nativeservice -run '^TestRealAccountLogoutStopsNative$' -v
```

## 接续工作

1. 官方账号稳定主体核验、合法认证目录/凭证交接，以及固定出口下的在线容器执行；当前 `executionReady=false`、`live_ready=false`。不能直接把宿主 keyring 或账号目录挂载到所有任务。
2. 账号绑定会话跨后端重启时，需要重新核验身份并建立显式受控绑定；本轮拒绝旧代际，不自动把旧会话交给重新登录的账号。现有离线会话恢复能力与持久记录保留。
3. 残留认证标记或容器清理债务的人工核对/修复流程，尚未开放自动清理按钮。重启会重新核对容器日志；不能用删除占用标记代替确认旧认证进程已停止。
4. 原生费用/预算接线、角色执行入口和真实账号的登录、keyring、刷新、编码及恢复验收。Claude 强制出口链路仍未就绪。

协议核对依据：[官方 Codex App Server](https://learn.chatgpt.com/docs/app-server) 的受管认证及 account/read 刷新说明；本轮没有启用外部 token 模式，也不把文档支持当作真实账号验收。
