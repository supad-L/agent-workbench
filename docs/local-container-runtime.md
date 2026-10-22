# 本机 Podman 环境

记录日期：2026-10-22。公司禁止 Docker Desktop，用户截图列出 Podman Desktop 为替代软件。Docker Desktop 已停止，刚安装的应用、安装包和 D 盘初始化目录已移到 D 盘废纸篓；其命令链接和安装器添加的 shell 配置已撤回。macOS 保护的 `~/Library/Containers/com.docker.docker` 目录仍有约 10 MB 初始化残留，未绕过系统保护删除。

## 安装位置

| 内容 | 实际位置 |
| --- | --- |
| Podman Desktop 1.30.5，Apple Silicon | `/Volumes/D/Applications/Podman Desktop.app` |
| Podman 引擎 6.1.4 及辅助程序 | `/Volumes/D/Podman/runtime-6.1.4` |
| 安装包 | `/Volumes/D/Downloads` |
| 虚拟机磁盘、镜像缓存和 SSH 身份 | `/Volumes/D/Podman/machine` |
| 命令链接 | `~/.local/bin/podman`，指向 D 盘引擎 |

`~/.local/share/containers/podman/machine` 是到 D 盘数据目录的符号链接。虚拟磁盘解析后的实际路径为 `/Volumes/D/Podman/machine/applehv/podman-machine-default-arm64.raw`。配置、套接字和桌面应用小型状态仍位于用户目录；不承诺系统盘零写入。

引擎从经官方 SHA-256 和 Apple 公证校验的安装包中提取到 D 盘；未运行默认安装器向 `/opt/podman` 安装。桌面应用也通过官方 SHA-512、签名和公证校验。桌面应用的自定义 Podman 路径已设为 D 盘二进制。辅助程序路径放在 `~/.config/containers/containers.conf.d/90-agent-workbench-external-runtime.conf`，避免桌面应用保存设置时覆盖。

## 运行与验证

- 虚拟机：`podman-machine-default`，Apple Hypervisor，rootless，4 CPU、4096 MiB 内存、64 GiB 磁盘上限。实际磁盘按数据增长；当前分配约 7.8 GiB，整个 D 盘 Podman 目录约 8.8 GiB（包含缓存与引擎）。
- 虚拟机仅显式共享 `/Volumes/D` 到同路径。未安装全局 Docker socket 的特权 helper，没有设置全局 `DOCKER_HOST`。
- `podman version` 已返回客户端和 Linux 服务端均为 6.1.4。
- `podman machine list` 已显示 Currently running，桌面程序已识别该机器。
- 拉取官方 Alpine 3.22，实际本地镜像 ID 为 `f28fb2fa6085390a967d9b74491b91c53cf00dec45bf1c7defa6aae11a52e519`。
- 实际容器 smoke test 通过：不可变本地镜像、禁止自动拉取、`--network=none`、只读根文件系统、UID 1000、移除 capabilities、no-new-privileges、资源限制和私有 `/tmp`；`/tmp` 写入成功，`/etc` 写入失败。测试容器已自动移除，没有遗留容器。
- 初次启动曾因 Ignition 用户初始化失败而出现短暂 running 状态但无法连接。保留诊断日志后重建空机器，从桌面应用启动；重建后的真实客户端/服务端连接与容器执行通过。没有将最初的状态文字当作验收。

后续已补充 Go 原型的双运行时选择，保留 Docker 默认行为，并使用 `--runtime podman` 完成真实写边界探针：允许目录可写、只读源树不可写、符号链接无法逃逸写入、容器根文件系统不可写、私有 `/tmp` 可写；探针容器及夹具清理通过，证据标记为 `real_podman`。临时夹具父目录为 `/Volumes/D/Podman/workbench-probes`。Docker 路线的自动测试保留，本机因公司限制不执行真实 Docker Desktop 测试。

这不等于完整任务执行器、CLI 沙箱集成或 Claude 专用代理出口通过。公司限制仅适用于本机，产品继续支持 Docker Desktop 与 Podman。操作说明见 [开发与运行](./development.md)。

新终端可使用 `podman machine list`、`podman machine start`、`podman machine stop`。D 盘必须在启动前挂载；运行时不要拔掉 D 盘。桌面配置的 CLI 路径和辅助程序配置需要在升级引擎后同步更新。

来源：[官方 macOS 安装说明](https://podman-desktop.io/docs/installation/macos-install)、[Podman Desktop 1.30.5](https://github.com/podman-desktop/podman-desktop/releases/tag/v1.30.5)、[Podman 6.1.4](https://github.com/podman-container-tools/podman/releases/tag/v6.1.4)、[Docker 兼容接口](https://podman-desktop.io/docs/migrating-from-docker/managing-docker-compatibility)。
