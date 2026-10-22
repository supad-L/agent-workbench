# M1 项目与方案基础交付记录

日期：2026-10-22。按用户“执行到项目 M1 完成”的指令交付本阶段，并保留 M0 尚缺的原生登录/隔离组合验收。M1 不包含真实主 Agent 对话或多 Agent 开发闭环。

## 实现与验收对应

| 条目 | 实现 | 验证 |
| --- | --- | --- |
| M1-01 骨架 | Go 回环服务、Electron、React/TypeScript、Ant Design、健康检查、独立应用包 | Go/前端构建、打包、实际 Electron 启动及重启 |
| M1-02 状态与事件 | SQLite v1、事务迁移、外键、WAL、状态与事件同事务、幂等、单实例锁 | 并发重试、冲突、外键失败回滚、重启恢复、新版数据库拒绝且不改表 |
| M1-03 项目资料 | 新建仓库、独立源码副本、未提交/未跟踪文件、排除清单、资料原件/文本/版本 | 原目录与副本独立、密钥/链接排除、Markdown/文本/PDF 正反例 |
| M1-04 方案版本 | 手工/合成草案、修订、内容哈希、人类确认、确认后不可变、变更提案 | 精确哈希/修订、数据库不可变触发器、V2 草案保留 V1、陈旧基线拒绝 |
| M1-05 权限 | 主进程持有人类令牌、项目域 Agent 令牌、后端角色判断、窄 IPC | Agent 确认 403、跨项目拒绝、伪造角色/外部 Origin/无效认证拒绝 |
| M1-06 对话/展示 | 手工消息持久化、方案编辑/查看/确认、资料展示、持久 SSE 重放/重连 | UI 组件操作测试及真实服务端到端验收；真实主 Agent 按原计划 M3 接通 |

方案业务暂集中于 `internal/store` 的事务方法，对话业务同样由 Store 承载，路由在 `internal/app`；职责保持分离，未为目录名额外建立空模块。M1 当前 HTTP 合同见 [本地接口说明](./m1-api.md)，内容结构继续使用版本 1 的既有 JSON Schema。

## 本轮可复现结果

- `python3 scripts/dev.py verify`：gofmt、vet、全量 `go test -race`、构建、十类合同及三协议/网络合成探针通过。
- `python3 scripts/dev.py lint`：0 issues；跨包 Go 语句覆盖率约 81%，以 `coverage.out` 为准。
- `python3 scripts/frontend.py test:desktop`：连接加密持久化、脱敏、修订冲突、同账号不同出口拒绝通过。加密单测采用测试加密器，不代表实际操作系统凭证迁移已验证。
- `python3 scripts/frontend.py test:ui`：协议选择与切换、格式检查不带密钥、保存清空密钥输入，以及确认 V1、提出 V2、保留 V1、保存消息通过。
- `python3 scripts/frontend.py build:ui`：类型检查及生产构建通过；Ant Design 主包仍有体积警告，未当作发布优化完成。
- `python3 scripts/m1-smoke.py`：真实 Go 进程和临时目录跑通 8 项验收，覆盖导入、资料/消息、权限、V1/V2、SSE 和服务重启。
- `python3 scripts/frontend.py package:mac` 与 `python3 scripts/desktop-smoke.py`：真实 macOS arm64 `.app` 打包、React→preload→主进程→Go 的启动握手、重启与锁释放通过；测试不驱动鼠标或检查视觉布局。

默认验证不读取已有模型凭证、不调用真实模型。三协议公司中转测试为本地合成 HTTPS/HTTP 夹具；用户先前回传的 DeepSeek 真实两轮通过记录保持“用户执行报告”来源，没有重新付费调用。

本地证据输出位于忽略的 `output/verification/m1-acceptance.json` 与 `desktop-startup.json`；打包位于 `output/desktop/mac-arm64/Agent Workbench.app`。二进制、数据、缓存和密钥不入 Git。

## 边界和后续

桌面附带通用自定义 API 配置页，属于用户优先要求的局部 M2 能力，不代表账号、调度、预算等整个 M2 已完成。API Key/代理/额外请求头由主进程加密保存，空输入保留已有值；真实公司私有服务仍需用户填写本地配置后测试。

Mac 锁屏期间未完成原生窗口外观与鼠标点击检查；已做实际应用启动和独立组件操作测试，不把二者写成截图或人工操作验收。

未完成项继续保留：GPT/Claude 原生订阅登录、Claude 强制住宅出口、完整多 Agent 派工/审查/返工/集成、真实项目自动交付、跨平台分发与签名公证。Podman/Docker 双运行时保持支持；M1 的资料与方案操作不运行用户源码，无需启动容器。

用户操作见 [中文指南](./工作台-中文操作指南.md)，构建见 [开发运行说明](./development.md)。
