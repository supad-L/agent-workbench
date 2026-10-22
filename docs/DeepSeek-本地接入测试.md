# DeepSeek API 本地接入测试

这是 agent-workbench 的 M0 接入验收入口，尚不是完整工作台界面。你已选择先验证 DeepSeek API；当前尚未配置密钥，也没有调用真实 DeepSeek。

## 1. 准备自己的 API Key

在 [DeepSeek 开放平台](https://platform.deepseek.com) 准备一个可用的 API Key，并确认账号有可用额度。网页聊天账号的登录状态不能替代 API Key。

密钥留在本机，**不用发到聊天里，也不用写进项目文件**。

## 2. 在终端运行本地测试

打开 Mac「终端」，依次执行：

```sh
cd /Volumes/D/supad/agent-workbench
python3 scripts/live-deepseek-probe.py
```

看到 `粘贴 DeepSeek API Key（输入不显示）` 后，粘贴密钥并按回车。输入时看不到字符是正常的。想取消就按 **Control + C**。

脚本只将此次输入的密钥传给当前测试子进程，不写入配置、命令行参数或 shell 历史；测试完成后不会保存密钥。脚本必须在你自己的交互终端运行，不能通过普通后台执行直接输入。

如果提示缺少编译文件，先执行一次，再重新运行测试：

```sh
python3 scripts/dev.py build
```

## 3. 这次测试会做什么

| 项目 | 本次行为 |
| --- | --- |
| 服务地址 | `https://api.deepseek.com/chat/completions` |
| 模型 | `deepseek-flash` |
| 协议 | Chat Completions，显式 DeepSeek 方言 |
| 模式 | 非思考模式；明确发送 `thinking.type=disabled` |
| 网络 | 跟随本机路由/TUN规则；不修改 Clash 或系统代理 |
| 调用次数 | 正常流程为两轮模型请求，不自动重试 |
| 输出限制 | 每轮 `max_tokens=256`；会消耗实际 API 额度 |
| 工具 | 让模型调用无外部副作用的 `probe(value=42)`，返回 42 后验证第二轮回复 |
| 项目内容 | 不上传项目代码，不读业务文档，不执行项目命令 |

配置模板位于 `configs/deepseek-api-probe.example.json`，里面没有密钥。此测试不依赖容器，因此只验证服务协议与当前宿主请求路径；不验证 Podman 中的 Agent 执行。

DeepSeek 当前默认开启思考模式，工具续接还涉及思考内容回传。本轮先固定非思考模式验证基本工具回合，不宣称已完成 DeepSeek 思考模式支持。[官方思考模式说明](https://api-docs.deepseek.com/guides/thinking_mode/)

## 4. 怎么看结果

成功报告中应包括：

```json
{
  "protocol": "chat_completions",
  "evidence": "explicitly_configured_endpoint",
  "requests": 2,
  "tool_verified": true
}
```

这是报告中的关键字段，实际输出还会包含用量是否已知。成功仅证明本次配置和两轮工具调用通过，不代表正式 Agent 调度、长期稳定性或其他模型均通过。

失败时可把**错误文字或去掉敏感内容的结果报告**发给我，密钥不用发：

| 现象 | 优先核对 |
| --- | --- |
| HTTP 401 | Key 是否正确、仍然有效 |
| HTTP 402 | API 额度或账号状态 |
| HTTP 429 | 当前服务限流情况 |
| 网络错误、超时 | 当前网络/TUN规则是否能到达 DeepSeek 官方 API |
| 没有完成工具回合 | 需要结合实际服务行为进一步适配，不能当作验证通过 |

接口与模型依据：[官方首次调用说明](https://api-docs.deepseek.com/)、[官方工具调用说明](https://api-docs.deepseek.com/guides/tool_calls/)。核对日期：2026-10-22。
