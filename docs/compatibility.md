# 版本与兼容记录

记录日期：2026-10-22。

| 组件 | 固定/观察版本 | 当前证据 |
| --- | --- | --- |
| Go | 1.27.3 darwin/arm64 | 本地编译与测试；归档 SHA-256 为 dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2，核对来源为 [Go 官方发布清单](https://go.dev/dl/?mode=json) |
| JSON Schema | github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 | go.mod/go.sum 固定，运行时禁用外部 schema 加载 |
| golangci-lint | v2.15.0 | 独立工具缓存，不作为产品依赖 |
| Node | 可用的本地依赖运行时 | 本阶段尚未使用，不代表前端已实施 |
| Docker / Docker Desktop | 保留原型适配与默认选择 | 本机安装路线因公司限制撤回；产品继续支持，自动测试通过，本轮不执行真实 Docker Desktop 测试 |
| Podman Desktop / Podman | 1.30.5 / 6.1.4，darwin/arm64 | 程序、引擎及虚拟机数据在 D 盘；工作台真实写边界探针通过，完整执行器/网络隔离仍待验收；见 [环境记录](./local-container-runtime.md) |
| Codex | 发现可执行文件，版本未调用探测 | 未读取认证或进行推理 |
| Claude Code | 未发现 CLI | 登录与工具执行未验证 |
| Messages API | 合成协议 v1 fixture | 不是任何具体模型/套餐的兼容声明 |
| Chat Completions | 合成协议 v1 fixture | 不是任何具体模型/套餐的兼容声明 |
| Responses API | 合成协议 v1 fixture | 保存 provider output 以续接工具结果；真实推理模型仍需验证 |
| DeepSeek API | 2026-10-22 官方文档，deepseek-flash / Chat | 显式 DeepSeek 方言使用 max_tokens 与非思考模式；两轮合成续接通过，真实 Key/服务仍待用户本机验收；见 [测试说明](./DeepSeek-本地接入测试.md) |
| 账号网络绑定与代理 | v1 合同和 Go HTTP 原语 | 代理/本机路径、故障不回退、独立会话撤销的回环实验通过；严格网络隔离、凭证库与正式配置服务仍未实现 |

参考文档：[Go 发布清单](https://go.dev/dl/)、[OpenAI 流式事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)、[OpenAI 工具调用](https://developers.openai.com/api/docs/guides/function-calling)、[Claude 流式消息](https://platform.claude.com/docs/en/build-with-claude/streaming)、[JSON Schema 库](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6)。实现遵循公开协议形状，合成内容均为本项目自建。
