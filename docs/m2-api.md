# M2 本地 HTTP 与桌面接口

当前实现：2026-10-23，`stage=M2`，SQLite schema 5。M1 项目/文档/方案接口继续保留，[M1 接口记录](./m1-api.md) 中的健康检查版本和连接测试请求格式由本页更新。`contracts/openapi.json` 仍是 M0 全阶段设计草案，不是当前全部可调用接口。

## 身份与秘密边界

服务仍只监听回环动态端口，除健康检查外需进程内 Bearer 令牌，禁止跨域。以下目录、运行、预算及诊断接口仅允许用户身份。桌面主进程持有人类令牌，渲染层使用 preload 白名单，不提供任意 URL 或 RPC 透传。

连接 metadata 中的 `account_id` 是加密库私有种子的 HMAC 标识，不是 API Key。`config` 中只放凭证引用，秘密值由主进程在运行或测试时临时传给后端，不保存到 SQLite 或返回给页面。配置中的 URL/请求头仍需遵守字段校验，秘密须使用引用。

## HTTP 路由

| 方法与路径 | 请求与结果 |
| --- | --- |
| GET `/healthz` | `{status:"ok",schema_version:5,stage:"M2"}`，无认证只读。 |
| GET `/api/connections` | 非秘密连接 metadata 数组。 |
| POST `/api/connections/check` | `apiconfig.Config`；校验格式并规范化端点，无模型请求。 |
| POST `/api/connections/validate` | `catalog.Connection`；校验 metadata，无模型请求。 |
| PUT `/api/connections/{id}` | `catalog.Connection`，ID 一致，修订不得倒退；同修订同内容可重放，不同内容冲突。保存新修订撤销旧连接运行。桌面加密库另行检查编辑所依据的修订。 |
| DELETE `/api/connections/{id}?revision=N` | 校验修订，角色引用存在则拒绝。 |
| POST `/api/connections/{id}/revoke` | `{}`，撤销活动运行。 |
| POST `/api/connections/test` | 见下方探针合同；固定两轮工具测试，共享用量/预算账本。 |
| GET `/api/roles` | 角色数组。 |
| GET `/api/role-templates` | `{templates,tools}`，模板默认停用且未绑定连接。 |
| PUT `/api/roles/{id}` | `catalog.Role`，新建 revision 0，编辑传当前 revision；返回递增修订。验证连接/模型与能力引用，保存后撤销角色旧运行。 |
| DELETE `/api/roles/{id}?revision=N` | 删除角色，保留历史运行。 |
| GET `/api/projects/{id}/runs` | 该项目的运行历史。 |
| POST `/api/projects/{id}/runs` | `{role_id,role_revision,input,scope,idempotency_key,connection_revisions,values}`，启动异步只读运行并返回 Run。 |
| GET `/api/projects/{id}/runs/{run}` | Run，包括快照、状态、检查点、预览和错误。 |
| POST `/api/projects/{id}/runs/{run}/cancel` | `{}`，请求取消并持久化状态/用量，返回 `cancel_requested`。 |
| POST `/api/projects/{id}/runs/{run}/resume` | `{values}`，显式恢复，保持快照及累计尝试上限，不重置费用记录。 |
| GET `/api/usage` | `{summaries,budgets,provider_balance:"unknown",subscription_quota:"unknown"}`。 |
| PUT `/api/budgets/{currency}` | `{currency,revision,enabled,limit_micros}`，支持 CNY/USD，返回递增修订。 |

连接模型字段为 `id/tools/currency/input_price_micros_per_million/output_price_micros_per_million/price_known`。`tools` 是 `unknown/supported/unsupported`，不是服务自动检测结果。模型价格的整数单位为“每百万 tokens 的百万分之一货币单位”。例如人民币 2 元/百万 tokens 填 `2000000`。预算 `limit_micros=10000000` 则代表 10 元。所有计价使用请求时快照，不拉取实时价格。

角色 `primary` 为 `{connection_id,model_id}`，`tools` 使用平台固定词汇。`fallback` 包含 `enabled/candidates/data_scope/currency/max_extra_cost_micros`，默认关闭。启用后需 1–4 候选、显式资料范围和正数上限；认证与权限失败不触发切换。

`scope` 为 `conversation_only` 或 `project_summary`。当前运行仅开放资料范围和角色工具交集内的四种平台只读工具，不执行代码。Run 状态为 `running/completed/failed/cancelled/interrupted`；重启将未结束运行标为 interrupted，不自动重发。角色或连接修订变化可阻止恢复。

## 连接测试合同变更

请求由 M1 的 `{config,values}` 改为：

```text
{
  config: apiconfig.Config,
  connection: catalog.Connection,
  values: {明确配置的凭证引用名: 秘密值},
  idempotency_key: 每次用户主动测试生成的唯一键
}
```

`connection.config` 必须与 `config` 一致。其他字段提供账号身份、并发上限、模型与价格快照；支持测试尚未保存的表单，测试本身不会保存其秘密。桌面加密库首次主动保存/测试时持久化身份种子，使相同凭证的未保存测试也能稳定共享账号并发限制。

服务固定提供无外部副作用的 `probe` 工具，验证一次工具请求和一次工具结果回传，禁止调用方扩展工具/输入。成功返回 `{protocol,evidence,requests:2,tool_verified:true,text:"",usage_known,run_id}`。同键同请求返回原结果，不再次发送请求；不同请求冲突。失败或断连也保留已有用量和未知预留，不能据失败推断没有费用。诊断使用隐藏内部项目，不出现在项目列表。

`values` 是主进程与本机服务之间的瞬时秘密载荷，不应手工写入公开脚本、日志或示例。普通页面只提交表单内容或连接 ID，由主进程解析引用。

## 预算与恢复语义

每个实际模型请求之前事务预约保守费用；同一运行和序号仅结算一次。报告值、估算值和未知值分开存储；未收到完整用量或取消时保留未确认预留。启用任意货币预算后，未知价格请求在预约前拒绝；已知币种按其启用的预算控制，不进行汇率换算。

预算为当前工作台账本的累计上限，无自动周期重置。降低预算不能小于已计费用加预留。此处不代表服务商余额或订阅额度，也不涵盖独立 CLI 探针发出的请求。

## 桌面 IPC

新增白名单操作：`roles.list/templates/save/delete`、`runs.list/get/start/cancel/resume`、`usage.get`、`budget.save`、`connections.delete`、`accounts.list/check/login/cancel/logout`。保留原 M1 操作。

`connections.save/list/delete` 在主进程同步加密库及非秘密目录；运行 start/resume 自动解析连接修订与秘密引用，页面不提交历史密钥。账号操作仅接受内建 provider，不能转发任意原生 RPC。当前 GPT 使用官方 app-server 的账号方法，Claude 返回未就绪；没有对应的通用 Go HTTP 账号登录端点，也没有原生编码线程入口。


## 原生会话只读记录（schema 5）

GET `/api/projects/{id}/native-sessions` 返回会话/本地恢复条件列表；GET `/api/projects/{id}/native-sessions/{session}` 返回 `{view,attempts}`。仅人类身份可读，跨项目详情拒绝。`view.can_resume` 是本地文件证据，`view.live_ready=false` 表示真实账号与在线执行尚未接通。没有开放原生启动或续接 HTTP 操作。详见 [恢复记录](./m2-native-session-recovery-2026-10-23.md)。

## 原生账号协调控制面（2026-10-24）

GET `/api/native-account` 读取保守共享槽状态；POST `/api/native-account/auth/begin` 使用 `{kind}` 领取认证所有权；POST `/api/native-account/auth/finish` 使用匹配的 `{operation,authenticated,owner_active,healthy}` 回执收尾。仅人类身份，严格拒绝未知字段和凭证；冲突返回 409。begin/finish 由桌面主进程调用，没有对应渲染层 IPC，也不启动任何模型。登录/退出等待原执行清理及持久收尾，检测/刷新不抢占执行。详见 [协调记录](./m2-native-account-coordination-2026-10-24.md)。
