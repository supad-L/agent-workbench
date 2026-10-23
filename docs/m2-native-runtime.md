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
