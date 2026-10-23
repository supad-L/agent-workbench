# 版本与兼容记录

记录日期：2026-10-23。M2 证据范围见 [实施记录](./m2-report.md)。

| 组件 | 固定/观察版本 | 当前证据 |
| --- | --- | --- |
| Go | 1.27.3 darwin/arm64 | 本地编译与测试；归档 SHA-256 为 dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2，核对来源为 [Go 官方发布清单](https://go.dev/dl/?mode=json) |
| JSON Schema | github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 | go.mod/go.sum 固定，运行时禁用外部 schema 加载 |
| golangci-lint | v2.15.0 | 独立工具缓存，不作为产品依赖 |
| Node / pnpm | 本机 v24.19 / v11.25 | 前端、桌面测试和构建脚本已使用；依赖由 pnpm-lock.yaml 固定 |
| Electron / electron-builder | 45.0.0 / 26.15.3 | macOS arm64 本地未签名应用打包、真实启动和重启通过 |
| React / Ant Design / TypeScript / Vite | 19.3.1 / 6.6.7 / 7.0.2 / 8.3.5 | 生产构建、类型检查和界面组件测试通过 |
| SQLite 驱动 | github.com/mattn/go-sqlite3 v1.14.52，CGO | 当前 schema 5；旧库事务升级、外键/WAL、用量预约和重启恢复测试通过；需要本机 C 编译器 |
| PDF 提取 | github.com/ledongthuc/pdf 5e46805f718e | 固定到 go.mod 中完整伪版本；带文字 PDF 夹具通过，无 OCR |
| Docker / Docker Desktop | 保留原型适配与默认选择 | 本机安装路线因公司限制撤回；产品继续支持，自动测试通过，本轮不执行真实 Docker Desktop 测试 |
| Podman Desktop / Podman | 1.30.5 / 6.1.4，darwin/arm64 | 程序、引擎及虚拟机数据在 D 盘；工作台真实写边界探针通过，完整执行器/网络隔离仍待验收；见 [环境记录](./local-container-runtime.md) |
| Codex | 本机发现官方 CLI 0.163.0-alpha.11；非产品捆绑版本 | 工作台独立空目录的 app-server initialize/account/read 通过；官方登录入口已实现，真实登录、刷新、隔离编码执行未验收 |
| Claude Code | 未发现 CLI | 登录与工具执行未验证 |
| Messages API | 合成协议 v1 fixture | 不是任何具体模型/套餐的兼容声明 |
| Chat Completions | 合成协议 v1 fixture | 不是任何具体模型/套餐的兼容声明 |
| Responses API | 合成协议 v1 fixture | 保存 provider output 以续接工具结果；真实推理模型仍需验证 |
| DeepSeek API | 2026-10-22 官方文档，deepseek-flash / Chat | 显式 DeepSeek 方言使用 max_tokens 与非思考模式；合成测试及用户回传真实两轮工具报告通过；见 [测试说明](./DeepSeek-本地接入测试.md) |
| 账号网络绑定与代理 | v1 合同、Go HTTP 原语、桌面 API 配置 | API 路径、代理故障不回退及撤销实验通过；API 凭证使用异步 safeStorage 加密；GPT 授权要求官方 CLI keyring，真实存取未验收；Claude 强制全链路隔离仍未就绪 |

参考文档：[Go 发布清单](https://go.dev/dl/)、[OpenAI 流式事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)、[OpenAI 工具调用](https://developers.openai.com/api/docs/guides/function-calling)、[Claude 流式消息](https://platform.claude.com/docs/en/build-with-claude/streaming)、[JSON Schema 库](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6)。实现遵循公开协议形状，合成内容均为本项目自建。
