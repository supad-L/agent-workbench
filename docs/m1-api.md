# M1 本地 HTTP 与桌面接口（v1）

本页保留 M1 交付时的接口记录。当前运行服务已升级至 M2/schema 4，健康检查及连接测试请求格式的变化见 [M2 接口说明](./m2-api.md)。`contracts/openapi.json` 是包含后续阶段路由的 M0 设计草案，不能据草案假定后续接口已开放。

服务仅监听 `127.0.0.1` 的动态端口。`serve --data-dir ABSOLUTE_PATH` 启动时向父进程 stdout 输出一行 `{url,human_token}`，属于本机秘密启动握手，不应复制进日志。桌面主进程私有保存令牌，渲染层仅通过 preload 白名单调用。

除健康检查外，请求必须使用 `Authorization: Bearer TOKEN`，JSON 请求使用 `Content-Type: application/json`；不提供跨域访问。Agent 令牌限定项目，无用户确认能力，也不能取得人类令牌。令牌进程内有效，重启失效。

| 方法与路径 | 身份 | 请求/结果 |
| --- | --- | --- |
| GET `/healthz` | 无认证，只读 | `{status:"ok",schema_version:1,stage:"M1"}` |
| GET `/api/projects` | 用户 | 项目数组 |
| POST `/api/projects` | 用户 | `{name,source,idempotency_key}`；source 空值新建，否则导入，返回项目 |
| GET `/api/projects/{id}` | 用户或本项目 Agent | `{project,messages,plans,documents}` |
| POST `/api/projects/{id}/messages` | 用户或本项目 Agent | `{text,idempotency_key}`；消息角色由后端身份决定 |
| POST `/api/projects/{id}/plans` | 用户或本项目 Agent | `{content,base_version,reason,idempotency_key}`；返回草案 |
| PUT `/api/projects/{id}/plans/{version}` | 用户或本项目 Agent | `{content,expected_hash,expected_revision,idempotency_key}`；只能修改草案 |
| POST `/api/projects/{id}/plans/{version}/confirm` | 用户 | `{expected_hash,expected_revision,idempotency_key}`；校验基线并确认 |
| POST `/api/projects/{id}/documents` | 用户 | `{path,idempotency_key}`；返回资料版本 |
| GET `/api/projects/{id}/events?cursor=N` | 用户或本项目 Agent | SSE，返回序号严格大于 N 的事件；支持 `Last-Event-ID` |
| POST `/api/projects/{id}/agent-token` | 用户，仅后端内部协调 | 返回项目域 Agent 令牌；桌面不提供此 IPC |
| POST `/api/projects/{id}/permission-demo` | 用户 | 安全权限演示，返回 Agent 确认拒绝信息 |
| GET `/api/plan-template` | 用户 | 明确标为合成示例的方案内容 |
| POST `/api/connections/check` | 用户 | `apiconfig.Config`，无密钥，仅语法/端点规范化，不请求模型 |
| POST `/api/connections/test` | 用户 | `{config,values}`，明确传入的凭证映射和两轮真实探针 |
| POST `/api/proxies/validate` | 用户 | `{url}`，返回脱敏 scheme/host/port/认证已配置标记，不连接代理 |

`content` 符合 `contracts/plan.schema.json`，服务端写入正确项目 ID、版本、schema_version 后验证。创建版本从 1 递增；已有生效方案时 `base_version` 必须等于当前版本，`reason` 必填。确认同时检查草案内容哈希、修订号及其基线仍然生效，避免并发草案覆盖新方案。

幂等键按身份、项目、操作隔离；同键同请求返回原结果，同键不同请求返回 409。成功变更及事件在同一 SQLite 事务中提交。事件包含 `sequence/project_id/kind/payload/created_at`；服务每批重放最多 256 条并继续读取，空闲时发送心跳。

错误为 JSON `{error:"可显示信息"}`；401 无有效认证，403 越权，404 记录不存在，409 修订/哈希/基线或幂等冲突，400 输入不合法或当前操作失败。未知路由和不支持的方法由标准路由器返回 404/405。请求 JSON 上限 1 MiB。

Electron IPC 操作名与 HTTP 不是一一公开代理：只允许 preload 列出的操作，主进程校验发送页面和项目编号。目录/文件选择使用本地对话框；`connections.list/save` 在主进程处理加密本地配置，HTTP 不开放凭证列表/保存。保存 DTO 只含配置引用和“已配置”标记，不回显密钥或认证代理 URL。
