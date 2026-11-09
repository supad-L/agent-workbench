# Codex CLI 镜像

供“订阅账号 → 执行环境与出口检测”及后续原生隔离执行使用的本地镜像。

- 基础镜像：`docker.io/library/node:22-bookworm-slim`（构建时记录实际 digest 为镜像标签）。
- 工具：npm 官方包 `@openai/codex`，安装后位于 `/usr/local/bin/codex`。
- 不含账号、密钥、认证目录或配置；认证交接属于后续单独验收项。

构建（在 macOS 主机上，需要 Podman 运行中并可联网拉取基础镜像和 npm 包）：

```sh
scripts/build-codex-image.sh                 # 安装 npm 上的最新版本
CODEX_VERSION=0.163.0 scripts/build-codex-image.sh   # 固定版本
```

脚本会：拉取基础镜像 → 构建 → 用与工作台检测相同的断网、只读、UID 1000 条件运行 `codex --version` → 打标签 `localhost/agent-workbench/codex:<版本>` → 输出镜像 ID（`sha256:…`）。把该 ID 填到检测表单的“本地镜像名称或 ID”。

镜像检测通过只说明工具存在且能离线运行，不代表官方来源已核验、账号已登录或可以进行编码执行。
