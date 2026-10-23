# M2 原生执行器实施记录

日期：2026-10-23。M2-03/M2-05 的内部实现进度，**不是原生订阅执行验收通过记录**。

## 有界离线容器执行

`internal/sandbox.Execute` 接收平台准备的独立源快照、独占可写目录、不可变本地镜像和容器内 argv，支持 stdin、逐行 stdout、总输出/单行上限、超时与取消。Docker/Podman 必须明确选择；没有宿主执行和引擎回退，不自动拉取镜像。镜像 entrypoint 被批准的 argv 覆盖。

沿用只读源树、目录写挂载、普通用户、只读根、资源限制、禁用 capabilities 和私有 tmp；HOME、CODEX_HOME 与缓存指向容器私有 tmp。当前固定 `network=none`，不挂载认证目录或宿主 HOME，不承接在线推理。输入上限 1 MiB、输出上限 16 MiB、时间上限 20 分钟，调用者还必须提供本次更小或相等的显式限额。

调用者负责提供已按读取权限导出的独立快照，不能传入用户原项目或平台/账号目录；当前只接受明确的完整快照读取范围 `read_paths=["."]`，不冒充已有 M3 快照导出或调度授权。写挂载沿用现有范围和别名校验。

取消、超时、输出超限、解析器拒绝及退出失败均使用独立清理上下文，查询并移除本次随机命名容器，然后再次查询确认。无法确认清理时返回 `uncertain`，调用方不能释放执行所有权。真实测试发现 Podman `rm -f` 默认仍等待 10 秒，已对 Podman 显式使用 `--time=0`；Docker 保留自身参数。原始 stderr 和引擎错误不向上透传。

## 验证

默认竞态测试覆盖 Docker/Podman 的合成生命周期、输入/输出边界、回调失败、取消、超时、清理失败和残留容器。合成 driver 不属于真实隔离证据。

本机 Podman 6.1.4 + 已有 Alpine 不可变镜像的显式离线测试通过：stdin/分块输出、授权目录写入、保护目录/符号链接/根文件系统拒绝、私有 HOME/cache，以及取消/超时/输出超限后的容器清理。夹具为 D 盘随机私有目录，测试结束清理；没有读取账号或发起模型请求。Docker 本轮仅做合成验证。

复现（默认 `go test` 跳过真实执行）：

```sh
AWB_TEST_SANDBOX_RUNTIME=podman \
AWB_TEST_SANDBOX_IMAGE=sha256:f28fb2fa6085390a967d9b74491b91c53cf00dec45bf1c7defa6aae11a52e519 \
AWB_TEST_SANDBOX_SCRATCH=/Volumes/D/Podman/workbench-probes \
go test -race ./internal/sandbox -run TestRealOfflineExecution -v
```

其他环境使用自己的显式本地镜像与共享目录。本项没有接入桌面执行入口，也没有改变 GPT/Claude 的 `executionReady=false`。在线出口、账号目录交接/单一刷新管理、原生会话持久化和真实编码证据仍待完成。

## Codex 离线原生适配与统一接口

`internal/runtime.NativeExecutor` 明确声明启动、context 取消、原生会话编号、结果、报告/未知 token 用量及恢复能力。`internal/runtime/codex.Executor` 通过上述容器执行器启动 `codex exec --json --ephemeral`，模型显式指定，项目上下文只走 stdin，不拼入 shell 或进程参数。Codex 自身仍使用 workspace-write 策略，外层容器提供写边界；Git 检查只为不含 `.git` 的独立快照显式跳过。

JSONL 归一化覆盖会话开始、回合、Agent 文本、命令/文件/MCP/搜索工具事件；不透传推理、命令输出、工具参数或原始错误。工具事件属于原生进度，不是 Tool Broker 的新授权。相同已完成 item 去重、冲突重放/非法顺序/未完成工具/协议变化拒绝成功；多字节文本按 UTF-8 边界分块，单事件最多 32768 字节。缺失/部分 usage 保持未知，明确零值仍为已报告用量。

只有提供方成功终态、进程零退出、容器清理核验及完成观察者都通过才返回 `completed`。截断、解析失败、异常退出或完成观察者失败返回 `uncertain`，context 取消/超时且完成清理后返回 `interrupted`。失败结果保留会话编号、草稿和已报告用量，不自动重试。平台任务验收仍由后续证据/审查决定。

当前私有 HOME 在容器退出时销毁，因此能力明确为：会话编号可报告，原生 `resume=false`、`live_ready=false`、token 硬上限不支持；恢复仅能由平台新建 attempt 并重建上下文。显式传入旧会话编号会在启动前拒绝，不使用 `--last`。这保留原生持久化恢复的后续需求。

协议依据已核对 [官方非交互模式](https://learn.chatgpt.com/docs/non-interactive-mode) 和 [官方 CLI 参数](https://learn.chatgpt.com/docs/developer-commands?surface=cli)。这不是 app-server 授权桥的替换，也未读取或交接 Electron 账号凭证。桌面原生执行入口继续保持禁用。

验证：合成 JSONL 覆盖成功、已报告零/未知用量、重复事件、工具失败、损坏/截断/不支持事件、异常退出、取消、超时、清理未确认、观察者失败、长中文文本分块与拒绝原生恢复。另在真实 Podman 中运行脚本夹具，验证适配器→容器→stdin/JSONL→统一结果→清理的联调；**脚本输出是合成 CLI 协议，不是实际 Codex 或模型执行**。复现使用前述相同三个显式环境变量：

```sh
go test -race ./internal/runtime/codex -run TestRealOfflineCodexFixture -v
```

上述包测试不提供桌面角色绑定、账号调度/交接、费用入账或正式任务恢复；这些必须在认证/出口与 M3 服务接通后按原方案验收。
