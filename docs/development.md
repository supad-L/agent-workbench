# 开发与运行

当前提供 M1 项目与方案功能，以及 M2 的角色、三种协议 API 运行、用量预算和官方 GPT 授权入口。M2 原生编程工具执行未验收，真实主 Agent、派工和审查闭环仍按后续阶段接入。原 M0 探针继续保留。

## 桌面运行与打包

本机已打包的 macOS arm64 应用位于 `output/desktop/mac-arm64/Agent Workbench.app`。在项目根目录双击 `启动工作台.command`，工作台数据写入同目录的 `data/desktop/`；项目在外置盘时数据随之保存在外置盘。直接双击 `.app` 则默认使用系统用户应用数据目录。当前包为未签名的本地开发包，尚未完成正式分发、公证和跨平台验证。

首次构建需要 Go、Git、C 编译器（SQLite 的 CGO 依赖）、Node.js 24 和 pnpm 11.25。macOS 可使用 Xcode Command Line Tools 提供编译器；本机已具备。前端脚本优先查找 PATH，再查找本机 Codex 依赖运行时，工具缓存放在仓库的 `output/tool-cache/`。

~~~sh
python3 scripts/dev.py build
python3 scripts/frontend.py install
python3 scripts/frontend.py exec install-electron
python3 scripts/frontend.py build:ui
python3 scripts/frontend.py desktop
~~~

首次 `install-electron` 和打包可能下载 Electron。生成可直接打开的本地应用及验证命令：

~~~sh
python3 scripts/frontend.py package:mac
python3 scripts/frontend.py test:desktop
python3 scripts/frontend.py test:ui
python3 scripts/m1-smoke.py
python3 scripts/m2-smoke.py
python3 scripts/frontend.py exec node scripts/native-auth-smoke.cjs
python3 scripts/desktop-smoke.py
~~~

`m1-smoke.py` 使用临时源项目和真实本地服务；`m2-smoke.py` 使用本机合成网关验证角色工具循环、连接测试记账、预算、幂等及重启，不请求服务商。`native-auth-smoke.cjs` 仅在全新工作台目录验证官方 Codex 握手和空账号状态，不登录、不推理。`desktop-smoke.py` 启动实际应用，验证渲染层握手、后端健康、退出和再次启动，不自动点击界面。临时夹具会清理，脱敏报告位于忽略的 `output/verification/`。仅修改文档时无需重新打包，修改 Go/前端/桌面代码后应先重建相应部分，再打包。

独立后端入口为 `./bin/agent-workbench serve --data-dir /ABSOLUTE/DATA_DIRECTORY`。它输出包含本地人类令牌的私有启动握手，不要把原始 stdout 上传；一般使用桌面入口即可。当前数据库 schema 为 5，旧数据库自动事务升级，升级后不应使用旧二进制打开。操作前可在应用退出后备份整个数据目录。HTTP 合同见 [M1 基础接口](./m1-api.md) 和 [M2 新增及变更接口](./m2-api.md)，操作见 [中文指南](./工作台-中文操作指南.md) 和 [角色与连接指南](./M2-角色与连接操作指南.md)。

## 环境

- Go 1.27.3；项目不依赖系统全局安装的 Go。
- Python 3 用于开发脚本和合同生成。
- golangci-lint v2.15.0 用于静态检查。
- 真实隔离探针支持 Docker（包括 Docker Desktop）及 Podman；需要启动所选引擎，明确指定已在该引擎本地的含 /bin/sh 的 Linux 镜像。探针不会自动拉取镜像。

macOS arm64 可运行：

~~~sh
python3 scripts/bootstrap-go.py
~~~

若官方归档下载路径不可达，可以显式选择 HTTPS 镜像目录；下载字节仍必须匹配已从 Go 官方发布清单核对并固定的 SHA-256。

~~~sh
python3 scripts/bootstrap-go.py --mirror https://mirrors.aliyun.com/golang
~~~

工具安装在用户缓存的 agent-workbench 专用目录，不修改 shell 配置。其他平台请安装相同 Go 版本。

## 本地命令

~~~sh
python3 scripts/dev.py verify
python3 scripts/dev.py lint
python3 scripts/dev.py doctor
python3 scripts/dev.py contracts
python3 scripts/dev.py protocol
python3 scripts/dev.py network
python3 scripts/dev.py build
~~~

verify 包含格式、vet、竞态检测、覆盖率、编译、合同检查、本机合成协议探针与回环 HTTP 网络策略实验。所有默认命令均不读取现有账号凭证或调用真实模型。coverage.out 和 bin/ 为忽略的本地输出。

构建后的命令：

~~~sh
./bin/agent-workbench doctor
./bin/agent-workbench contracts-check
./bin/agent-workbench protocol-probe
./bin/agent-workbench network-probe
./bin/agent-workbench sandbox-probe --image YOUR_LOCAL_LINUX_IMAGE
./bin/agent-workbench sandbox-probe --runtime podman --scratch-dir /YOUR_SHARED_PROBE_DIRECTORY --image YOUR_LOCAL_LINUX_IMAGE
~~~

## 容器运行时选择

Docker 与 Podman 是并列支持的后端，用户的公司禁用 Docker Desktop 不构成产品全局禁用。省略 `--runtime` 时保留原有 Docker 默认值；显式选择 `--runtime docker` 或 `--runtime podman` 后，失败不会自动换引擎。`doctor` 分别列出两种引擎，某一种不可用不代表另一种不可用。

工作台直接调用所选 CLI，因此 Podman 路线不要求启用全局 Docker 兼容 socket，不创建 docker 命令别名、不修改全局 Docker context 或 DOCKER_HOST。其他使用 Docker API 的工具是否需要 Podman 的兼容设置，应按那个工具单独配置。

`--scratch-dir` 指向已经存在、允许引擎访问的绝对目录。探针只在里面创建随机私有子目录并清理，不挂载整个父目录。macOS 的工作目录和探针目录需要出现在相应虚拟机的共享范围内。本机 D 盘已经共享，可运行：

~~~sh
python3 scripts/dev.py sandbox --runtime podman --scratch-dir /Volumes/D/Podman/workbench-probes --image f28fb2fa6085390a967d9b74491b91c53cf00dec45bf1c7defa6aae11a52e519
~~~

新终端已包含本机 `~/.local/bin/podman` 命令路径。上述目录及镜像是本机验证环境，其他用户应替换为其本地已准备的目录和镜像。Docker 和 Podman 的镜像存储独立；一个引擎里的镜像不会自动出现在另一个引擎中。

这是隔离原型的 CLI 入口，正式任务执行器与运行时设置界面仍待后续阶段实现。M1 的项目/资料/方案功能不执行用户源码，无需先启动容器。后续容器创建、挂载、资源限制及清理由程序负责；模型账号的代理配置属于工作台连接配置。

## 手动验证公司中转或其他真实 API

用户已回传 DeepSeek 真实两轮工具回合通过的本机报告，未重复调用。桌面“模型连接”支持自行选择协议、地址、认证、网络和额外请求头。CLI 专用模板仍为 `configs/deepseek-api-probe.example.json`，在交互终端运行 `python3 scripts/live-deepseek-probe.py` 可隐藏输入 Key 并发出两轮测试；不会保存密钥。使用显式 DeepSeek Chat 方言、`max_tokens` 和非思考模式。具体操作见 [DeepSeek 本地接入测试](./DeepSeek-本地接入测试.md)。

configs/api-probe.example.json 提供配置格式。复制到忽略的 config.local.json，填写实际协议、Base URL/完整端点、模型和认证方式；密钥只从你明确设置的 AWB_ 环境变量读取，不会自动读取 OpenAI/Claude 现有登录文件。

~~~sh
./bin/agent-workbench api-probe --config config.local.json
~~~

这个命令会向配置的服务发送两轮模型请求，可能消耗套餐或费用。唯一工具是无外部副作用的 probe，不读项目文件，不执行命令。报告只显示工具回合与用量是否可知，不回显原始模型文本、密钥或代理密码。独立 CLI 探针不接入桌面 SQLite 的预算和账本；桌面“测试连接”和“角色试运行”则统一经过账号并发、预算预约及用量结算。

网络 mode=system、dns_mode=system 对应本机 TUN 路由。若选择显式代理，改为 mode=explicit_proxy、dns_mode=proxy，并用 proxy_env 指向你配置的 AWB_ 代理 URL 环境变量。enforced_proxy 当前明确返回未就绪，普通代理测试不能替代 Claude 登录浏览器和 CLI 的强制出口验收。

sandbox-probe 检查所选 Docker/Podman 引擎与本地镜像，使用不可变镜像 ID，在私有临时目录运行真实文件边界测试。缺少条件返回 blocked 并以非零状态结束；不换引擎或回退到宿主执行。测试目录只包含合成文件。

## 合同与模糊测试

~~~sh
python3 scripts/generate-contracts.py
go test ./contracts ./internal/contractguard
go test ./internal/runtime/direct -run '^$' -fuzz FuzzSSEFraming -fuzztime 5s -parallel 2
~~~

使用本机 Go 时可把 go 替换为专用缓存工具链中的 go。生成后检查 schema、examples、OpenAPI 的差异。JSON Schema 是结构校验，内部 guard 提供跨文档检查；两者均不替代实际的用户认证、数据库授权或语义审查。

## 验证证据

`python3 scripts/frontend.py exec node scripts/native-account-smoke.cjs` 使用新建空认证命名空间，将真实 Go 后端与官方 Codex 账号桥接通，检查账号槽及认证进程停止。需先构建后端，不登录、不调用模型、不读取用户原有认证目录。真实容器内的账号退出取消探针见 [账号运行协调记录](./m2-native-account-coordination-2026-10-24.md)。

建议保存以下输出到 output/verification/，该目录不提交密钥或真实账号日志：verify、lint、doctor、protocol-probe、contracts-check。当前真实接入和容器隔离状态见 m0-report.md。

`network-probe` 仅验证应用 HTTP 路径、代理故障不回退及会话撤销，不证明 Claude 浏览器/CLI 或 IPv6/DNS/UDP 的强制隔离；`strict_isolation` 仍为 `unverified_blocked`。进度见 [网络实施记录](./network-proxy-progress-v0.2.md)。
