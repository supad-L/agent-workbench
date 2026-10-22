# 技术设计与执行规则

版本：0.1，设计草案。需求编号 R01–R10 见 [README](./README.md)。本文定义目标行为，不代表这些功能已经实现。

## 1. 范围与默认假设

### 1.1 首版范围

1. 本地 Web 工作台、本机后端、单用户多项目。
2. 项目持续对话、文档导入、方案确认和变更管理。
3. 可配置角色、模型 API 与编程 Agent 接入。
4. 并行任务、依赖调度、成果审查、返工和受控集成。
5. 连接、凭证、模型能力、用量、预算和故障状态管理。
6. 澄清问题、用户回答、局部等待和服务重启恢复。

### 1.2 工程假设与明确非目标

- 首要开发环境为 macOS，隔离代码执行依赖本机 Docker 运行时；Linux 同时验证，Windows 原生适配后续进行。
- 首版不包括团队多租户、公网托管、手机端、插件商店、自动生产发布，也不强制开发原生桌面外壳。
- 用户目标仓库默认保持原状；开发在平台管理的副本和交付分支中进行。推送、部署等外部动作按项目明确授权处理，默认交付本地成果。
- 默认不引入任意层级子 Agent。主 Agent 派任务，执行 Agent 可以提出拆分建议，实际派工仍由平台与主 Agent 处理。
- Docker、Git、CLI 与前端依赖版本在 M0 验证后锁定并登记，不依赖运行中的自动追新。
- 主 Agent 的原始推理无需展示；用户可见的是任务依据、工具活动、成果、审查结论与决策记录。

## 2. 核心对象与职责

| 对象 | 职责 | 不拥有的权限 |
| --- | --- | --- |
| 用户 | 确认方案、回答歧义、提出变更、控制运行 | 无需逐次批准普通派工和返工 |
| 主 Agent | 沟通、规划、选角色、审查、协调、汇报 | 不能修改已确认方案，不能自行新增验收目标，不能直接修改项目代码 |
| 角色模板 | 提示词、能力、工具权限、默认连接及模型 | 不持有真实密钥；角色不等于账号或运行实例 |
| 执行实例 | 执行一项任务、报告结果或阻碍、处理修改意见 | 不能自批、改方案、扩大写范围或递归派工 |
| 审查实例 | 可选的只读专业评审，主 Agent 汇总正式结论 | 不修改被审代码，不自动引入新产品要求 |
| 编排器 | 状态机、租约、调度、预算、问题影响传播 | 不替代模型作业务含义判断 |
| 工作区与集成服务 | 准备副本、生成差异、执行验证、串行集成 | 不信任 Agent 自报的改动和测试结果 |

架构评审、测试等角色可按项目需要启用；首版不要求每项小任务都走一整套额外角色。

## 3. 总体架构

~~~mermaid
flowchart TD
  UI[本地工作台] --> API[Go HTTP API 与事件流]
  API --> Conversation[项目对话与方案服务]
  API --> Connections[账号与连接服务]
  Conversation --> Coordinator[主 Agent 与编排器]
  Coordinator --> Scheduler[依赖调度与预算控制]
  Scheduler --> Runtime[统一 Agent 运行接口]
  Runtime --> Direct[直接 API Agent]
  Runtime --> External[Codex / Claude 等适配器]
  Direct --> Tools[受限工具代理]
  Tools --> Sandbox[任务隔离执行环境]
  External --> Sandbox
  Sandbox --> Artifacts[成果与验证服务]
  Artifacts --> Review[固定方案版本的审查]
  Review --> Integration[串行集成与回归验证]
  Review --> Scheduler
  API --> DB[(SQLite 与事件记录)]
  Connections --> Vault[本机凭证存储与认证适配器]
~~~

技术选型建议：Go 后端复用 agentbox 的工程经验，React + TypeScript 构建工作台；HTTP 处理操作，SSE 推送状态与文本，终端交互确有需要时使用 WebSocket。单机 SQLite 加事务事件表足以支撑首版，无需预先引入分布式队列。

### 3.1 建议代码目录

~~~text
cmd/agent-workbench/
internal/project/          项目与资料
internal/conversation/     会话与上下文组装
internal/plan/             方案版本、确认与变更
internal/task/             合同、轮次和成果
internal/orchestrator/     主 Agent 控制与工具
internal/scheduler/        依赖、资源和租约
internal/review/           正式意见、建议和复验
internal/clarification/    疑问与受影响任务
internal/connection/       账号、协议、模型能力
internal/credentials/      凭证与认证状态
internal/runtime/          通用运行接口
internal/runtime/direct/   Messages / Chat / Responses
internal/runtime/codex/    官方客户端协议适配
internal/runtime/claude/   官方允许的 CLI / SDK 接入
internal/toolbroker/       文件、命令和平台工具授权
internal/workspace/        基线、副本、范围和快照
internal/sandbox/          Docker 执行器与策略编译
internal/integration/      候选集成、验证和分支推进
internal/usage/            用量、预算和估算
internal/store/            事务、迁移与事件
web/                      React + TypeScript
contracts/                JSON Schema、OpenAPI、事件契约
tests/fixtures/           合成模型和 CLI 协议样本
docs/                     需求、设计和验证证据
~~~

### 3.2 复用策略

agentbox 本地审阅基线：15de6c6616592c12688bc6841969efa593a0710b，审阅时受跟踪文件无修改。参考凭证服务、代理出口、执行适配与用量管理；当前主管委派为顺序任务且主角色受 CLI 类型限制，不直接当成我们的编排核心。

实际复制源码时保留项目 LICENSE、版权与适用 NOTICE，登记来源和修改。先提取接口清晰的实现，避免同时引入多用户、反向隧道、远程桌面和 Linux 运维结构。dsh-agent-teams 作为任务状态与验收思路参考，实施前固定源码版本；本次未运行该插件。

## 4. 端到端工作流程

### 4.1 从需求到执行

1. 导入项目或建立空项目；记录当前基线、资料来源与排除项。
2. 用户与主 Agent 讨论；主 Agent 形成需求、非目标、流程、架构、验收、待确认问题。
3. 方案中必须包含自动化程度、异常恢复策略、哪些事项需要用户回答；不能留给执行时随意补充。
4. 阻塞问题得到答案后，生成方案草案和任务依赖图；每项任务关联需求与验收条目。
5. 用户明确确认后，平台保存不可变方案版本和确认记录，再允许派工。
6. 主 Agent 在该版本内生成任务合同、分配角色；平台校验覆盖、范围冲突、连接能力和预算。
7. 调度可运行任务。先集成共享接口或脚手架等真正的前置成果，再并行开发独立模块。
8. 子 Agent 提交成果，平台生成实际改动清单并运行声明的验证。
9. 主 Agent 审查具体成果版本；不通过则生成带问题编号的新执行轮次。
10. 通过后进入串行集成；合并组合验证通过才解锁下游。
11. 所有需求均有成果和证据覆盖后，汇总交付分支、差异、验证、成本与已知限制。

用户确认方案后，派工、常规验证、审查、返工和集成自动进行，不新增逐任务人工批准节点。

### 4.2 持续对话与新需求

执行中用户可以继续聊天。消息默认用于讨论，不自动改写当前执行依据。主 Agent 将明确的新要求组织成变更提案，列出增加、删除、受影响任务与已有成果。

用户明确确认的新增要求可作为对应变更的授权，不必额外重复确认同一事项。平台记录该次输入、变更范围和新版本；不能把对一个问题的回答扩大解释成批准整组建议。

确认新版本时，事务内更新当前方案指针和 plan_revision；仅对受影响任务及下游增加 task_epoch、撤销旧租约。project_run_epoch 用于项目整体停止/恢复，不因普通方案修订一律失效，否则会误停未受影响任务。

旧成果承接用 PlanAdoption 记录：旧 task/attempt/artifact 摘要、旧/新方案版本、对应需求/接口/工程基线摘要、复用判断及新方案下的审查引用。只有依据内容、执行范围和验证要求均未改变且成果仍在当前集成基线中，才可让旧 integrated 任务满足新版本依赖；不改写其历史版本。

未受影响的在途任务可在相同合同下继续，但须先有针对合同的新方案兼容绑定。成果产生后仍按新方案审查并建立具体成果的承接记录，不能提前承接未知成果。依据或验证发生变化时，不作兼容复用，撤销执行并创建新合同/任务。无法证明无影响时先暂停相关分支，主 Agent 补充分析或提问。

### 4.3 澄清

子 Agent 报告疑问 → 主 Agent 查阅固定方案和已有决策 → 能确定则给出有依据的执行答复 → 仍不确定则创建用户问题。

用户问题包含上下文、缺失信息、选项、推荐理由、受影响任务和等待期间可继续的事项。暂停依赖该答案的任务及其下游；其他独立任务继续。没有默认答案代替用户确认，也不因用户未回复而自动改变产品流程。

ask_coordinator/ask_user 为持久化、异步工具：保存问题后立即返回问题编号。提问的子 Agent 必须在安全点结束当前运行，持久化草稿，停止任务沙箱中的活动执行并释放账号槽；主 Agent 答复或用户回答后创建新的执行尝试续接。主 Agent 需要回答同一账号的子任务时不能被等待中的子 CLI 占槽阻塞。停止前的草稿仅为恢复材料，不当作已验收成果。

用户答复后，若只是解释原条款，生成关联该条款的不可变澄清记录，并提升受影响任务合同修订；若改变可观察行为或方案要求，形成由该答复授权的新方案版本。两种情况都撤销不再匹配的执行租约，通知相关成员并保存原文。

### 4.4 服务异常与返工上限

网络暂时失败使用有限重试；额度不足、认证失效、预算耗尽分别进入可见状态，按已配置策略等待、切换或停止。异常和内容审查失败分开计数。

建议默认：每项任务最多 3 轮正式审查，每轮瞬态传输最多重试 2 次；不取得新证据就重复相同意见时判为未推进。替代实现、换执行者和集成冲突修复都使用剩余轮数，必须复审，不能在第 3 轮失败后自动产生第 4 轮。达到总上限且仍需修改时，主 Agent 总结阻碍并向用户说明；若用户随后明确授权增加轮数或调整方案，记录授权和新上限，历史计数保留。不得复制任务或问题组绕过限制。

这里的报告与澄清属于开发工作台协作，不给目标软件新增人工审批功能。

## 5. 防止主 Agent 擅自改变方案

### 5.1 不可变方案

PlanVersion 保存需求、约束、非目标、架构决定、接口合同、验收条目、验证规则和自动化策略，并计算内容哈希。主 Agent 只拥有草拟和提出变更的工具。用户确认 API 单独鉴权，Agent 工具凭据不能调用确认入口。

任务、成果和审查绑定 plan_version、plan_hash、contract_revision 与执行基线。执行记录不覆盖旧版本。

### 5.2 正式意见与建议

正式阻塞意见只能依据：

- 已确认需求、接口或验收条目；
- 方案中已确认的工程基线，例如功能正确、构建与测试有效、保持既有兼容行为；
- 实际违反这些条款的证据。

新增功能、个人偏好、可选重构进入 Suggestion，无权阻塞交付，不产生修复任务。发现此前漏写的产品要求，必须提问或提案，不能借“正确性”扩展需求。对于原功能自身的缺陷，可以依正确性基线要求最小修复。

每条阻塞意见含 basis_id、artifact_id、证据、问题、最小修复要求和复验方式。程序检查依据存在、版本匹配和字段完整；语义是否匹配由审查判断和记录支撑，不能以“填了编号”声称完全防止漂移。

### 5.3 审查范围保持稳定

新一轮重点检查前轮问题是否修好以及修复引入的回归。新发现的真实缺陷仍可提出，但必须沿用同一方案依据并提供新证据。单纯换写法、换框架、加人工确认等不能反复作为通不过的理由。

主 Agent 自身没有直接写代码或改正式合同的工具。需要小修也派带范围的修复任务，不允许绕过审查自行更改最终成果。

## 6. 账号、模型与执行器

### 6.1 分离配置对象

Connection：服务商、认证类型、协议、地址、凭证引用、出口、能力与限额。

AccountResource：跨连接统一的认证主体、刷新链所有者和额度资源组。官方认证返回的 issuer/account/workspace 标识经验证后用于归一；API Key 使用服务端 HMAC 指纹识别重复凭证，刷新链使用稳定 chain_id 而非会变化的令牌值。连接别名和角色不会产生新的账号槽。额度域无法识别时标为未知，并对同一服务入口采用保守共享限额；跨域上游关系不明时不能宣称已完整识别共享额度。

RoleTemplate：职责、系统提示词、工具策略、默认执行器、连接和模型、允许备用连接。

AgentSession：某角色在某项目任务下的运行实例，保存原生会话编号和上下文版本，不在角色模板内存密钥。

同名模型在不同服务地址下能力可能不同，能力以连接内模型配置和验证为准。不得根据模型名称猜测工具兼容、额度或真实权益。

### 6.2 自定义 API 与 Coding Plan

表单字段：名称、服务商标签、协议、地址类型（Base URL / 完整端点）、地址、API Key 引用、额外请求头、模型列表、默认模型、推理参数、超时、并发、预算、出口。

首版协议适配目标：Anthropic Messages、OpenAI Chat Completions、OpenAI Responses。具体服务必须验证流式响应、工具调用、工具结果回传、上下文、取消和错误形状；协议兼容不等于所有参数兼容。

地址规范化后显示实际请求端点；完整端点不再拼接路径；拒绝把 /v1 或 /messages 重复追加。自定义地址须用户明确配置，HTTP 仅用于明确选择的本地服务；其他连接默认 HTTPS。

连接测试按证据展示：配置格式 → 认证/模型发现（服务支持时）→ 小型推理 → 工具回合。需要实际模型调用的测试标示会消耗套餐或费用。生产任务不在后台擅自跑周期保活或付费探测。

仅能文本响应的连接可以用于讨论或总结，不能被配置为需要工具执行的主角色或开发角色。套餐限制填写成连接策略，服务未提供额度接口时显示未知。

### 6.3 GPT 与 Claude 登录

GPT：优先验证官方 Codex 的账号登录和应用集成接口；适用时采用官方面向开源、本地应用的 Sign in with ChatGPT。明确显示认证方式和当前计费来源，不把 API Key 费用算作订阅额度。

Claude：保留账号登录需求，分别验证官方工具原生登录和第三方产品可用的授权方式。官方 SDK 文档对第三方提供 claude.ai 登录有限制；M0 必须记录适用接入方式和边界。不能以复制官方客户端身份、搬用订阅令牌或仅能完成 OAuth 为“生产支持”证据。

优先让官方运行时管理自己的认证与刷新；工作台管理状态、执行生命周期和授权引用。自建 OAuth 仅用于官方明确开放、已符合条件的路径。与 agentbox 的具体差异需写入适配说明。

### 6.4 凭证与出口

- API 密钥保存在本机凭证库或独立加密存储，数据库保存引用；加密密钥与数据库分开保存，导出默认不包含密钥。
- 直接 API 请求在服务端使用密钥，模型上下文与普通任务工具看不到密钥。
- 原生 CLI 必须取得其合法执行所需凭证时，使用账号专属认证目录，只暴露给当前执行者，不向其他任务挂载。该执行者的代码可能读取自己可访问的凭证，不能宣传成针对恶意代码的密钥绝对隔离。
- 每个凭证刷新链只有一个明确管理者。服务器不与活动 CLI 各自刷新同一链；服务端锁本身不能锁住外部 CLI 的刷新行为。
- 登录型 CLI 认证主体/刷新链默认并发 1，跨 Connection 别名统一计数。终止旧实例并确认收尾后，才能交接认证目录。只有对应版本验证过共享刷新与并发行为，才允许提高该账号资源组并发。
- 切换账号要停止旧执行、保存更新的认证状态、清除旧执行配置，再交给新账号；进程内存不会因修改配置文件自动更新。
- 同一连接的服务请求共用出口配置；指定出口故障时该路径失败，不自动改变出口。CLI 环境代理只对遵循配置的请求生效；未进行网络层约束时不声称所有流量都被强制代理。
- 日志保留结构化错误码和请求编号，过滤 Key、Cookie、授权码及令牌。

### 6.5 用量与备用

分别记录 provider_reported、estimated、unknown 三类用量与价格快照。订阅限额、服务端计量和用户自定预算是不同字段，未知不能当作零。

备用策略限定候选连接、模型、最大费用和可发送的项目资料范围。默认关闭自动跨服务商切换；用户预先启用后可以自动切换，不每次询问。认证/权限受限不进行无穷重试，不以自动轮换账户绕过服务规则。

切换执行者使用方案、合同、成果、问题与验证证据重建上下文，原生私有会话状态不保证跨模型迁移。替换后继续的是同一逻辑任务的新执行尝试，历史与费用保留。

## 7. 并行开发与实际修改边界

### 7.1 三层边界

| 层次 | 实现 | 可以验证的保证 |
| --- | --- | --- |
| 项目隔离 | 每个任务独立副本与执行环境 | 普通执行工具不能访问用户原项目、其他任务或平台数据 |
| 执行期写范围 | 只读源树、声明目录的可写挂载、受限工具代理 | 普通文件写入和命令写入不能修改未授权源目录 |
| 成果与集成 | 实际文件清单、内容摘要、路径检查、串行集成 | 未授权、陈旧或未审查成果不能进入交付分支 |

Git worktree 只用于版本与副本管理，不是操作系统权限边界。文件系统约束可测试，业务语义偏离仍依赖审查。

### 7.2 可执行的目录约束方案

1. 服务在受控目录创建基线提交与任务 worktree，按 read_paths 导出独立源快照给沙箱；需要读全项目时显式声明项目读取范围，不能以“只读”为由忽略读取权限。Agent 不获得可写的工作台 Git 管理目录。
2. 沙箱源树默认只读。合同声明的目录使用该任务独占的可写目录覆盖挂载，不能挂载其他任务或原项目目录。
3. 可写目录之外仅允许任务私有 HOME、临时目录、依赖和构建缓存；这些运行目录不进入交付成果。构建输出路径在项目执行配置中声明，禁止把源码目录作为可忽略缓存。
4. 首版运行期硬约束以目录为粒度。公共单文件修改通过平台 patch 工具提交，由服务校验完整路径、内容基线和合同，写入该任务独立副本后重新生成快照；不依赖单文件 bind mount 支持编辑器原子替换。补丁导致挂载重建时，先在安全点保存当前任务可写目录并停止执行，将目录改动和补丁组合成新快照再续接，不能丢失未提交改动或在活动进程下替换基线。
5. 补丁入口使用不跟随符号链接的目录句柄并复查目标；校验新增、删除、重命名、文件类型和权限变化。禁止路径逃逸、链接越界、大小写别名绕过和修改平台元数据。
6. 容器使用普通用户、只读根文件系统、固定挂载、资源限制；不挂载 Docker socket、宿主 HOME、平台数据库、其他账号目录或任意宿主目录，不开放特权模式。
7. 自带原生文件/命令工具的外部 CLI 必须在同一受限沙箱内运行；不能只检查平台自己的工具后，把 CLI 放到宿主机裸跑。
8. Git 的写入与集成操作由平台管理。任务需要 Git 只读信息时使用受限工具；依赖 .git 的构建由平台提供只读元数据兼容方式并列入项目环境验证，不通过放开宿主 Git 元数据解决。
9. M0 必须实测 Linux VM 中只读根和可写子目录挂载的组合、缓存写入、符号链接及构建行为。能力不满足就标为不可执行，不静默降低边界。

目录内更细的单文件允许列表由 patch 代理约束；确需任意命令写入时只开放批准的目录。合同要明确“目录级执行权”与“更细的交付范围”，不能把事后拒绝提交说成执行时就无法写入。

### 7.3 并发与共享文件

调度器同时检查任务依赖、目录所有权、共享资源锁、账号执行槽、机器资源和预算。目录前缀有重叠的写任务不能并发；公共接口、数据库迁移、根依赖与锁文件使用明确资源键串行处理。

按接口合同拆分前后端开发：先形成已确认接口并集成到基线，前后端引用其摘要并行开发。角色名称不作为无冲突的证据。

平台默认最多 3 个开发任务并行，主 Agent 单独维护逻辑会话；实际并发受连接与机器配置收窄。账号槽覆盖该账号的所有模型和角色。主 Agent 派工工具异步返回任务编号，主回合结束后释放执行槽，等待成果期间不占着同账号槽阻塞子任务；禁止使用持槽的长时间同步委派等待。

### 7.4 真实改动与验证

提交时先结束写入阶段、停止沙箱中的全部任务进程，再将任务副本及可写目录归档为不可变成果。确认没有后台写者后，服务比较任务基线与完整源树快照，包含未跟踪文件、删除、重命名、文件模式及链接变化。Git diff 是辅助视图，不是唯一清单；忽略规则不能隐藏越界改动。缓存排除项来自预先验证的执行配置。

验证命令在固定成果的隔离验证副本中运行，记录命令、环境镜像、输入摘要、退出码和日志摘要。验证期间生成的源文件变动需要重新形成成果或判为无效，不能把“验证前的审查”用于不同内容。

VerificationProfile 必须固定 runner、验证镜像、正式验收测试包、配置和摘要。正式测试包由平台在只读验证目录挂载，入口不委托候选代码中可修改的 package.json/scripts 或测试配置；候选自己的构建和测试也运行，但单独记为辅助证据。确需调用项目脚本时，脚本及其配置进入受保护基线并核对摘要。新增项目测试可以随实现提交，经审查后进入后续基线；不能自动替换本轮正式测试。调整正式测试依据走合同/方案授权，测试数量减少、跳过或日志格式不符不能自动算通过。

### 7.5 串行集成

1. 一次只集成一项通过审查的成果，锁定项目集成游标。
2. 检查当前方案版本或有效 PlanAdoption、合同修订、任务执行代际、成果摘要、正式意见和验证证据。旧成果仅凭 plan_version 不同不能自动通过或一律拒绝，必须按承接规则判定。
3. 在当前交付分支头建立候选集成副本；应用已批准的差异，检测冲突。
4. 自动重放无冲突差异后仍生成组合成果摘要，执行相关测试与接口检查；并发成果使用了旧公共接口时重新验证。
5. 冲突或组合回归将原逻辑任务从 integrating 转为 changes_requested，以新合同修订和新 integration_repair 尝试修复。输入为原候选成果及当前交付分支头，基线为当前分支头，不添加对原未集成任务自身的完成依赖。修复仍受原方案和协调后的范围限制，经过完整验证及正式复审；复审计入同一任务上限。集成服务不自行编写未审查的新代码。
6. 最终差异和验证针对候选提交。耗时验证期间不持项目最终提交锁；验证完成后，集成服务取得与方案激活、合同修订、停止/恢复共用的项目栅栏锁，重新核对 expected_plan_revision、有效承接、project_run_epoch、task_epoch、contract_revision、成果及验证摘要、expected_head。任一不符则废弃本次提交许可并重建候选，不能只依赖分支头 CAS。
7. 在上述锁内写入并持久化集成操作日志，比较并交换推进交付分支，更新 SQLite 结果和操作完成标记，完成后释放锁。Git 与 SQLite 仍不构成一个事务。服务启动必须先在同一项目栅栏下恢复未完成操作，再受理方案激活或新集成：核对记录的授权版本/代际/分支头，确认已推进则补记、未推进则重试可幂等步骤，状态不明则报告 uncertain。保留旧分支头及恢复记录。

只有当前方案内的 integrated 任务，或已有有效 PlanAdoption 的旧 integrated 成果，满足正式下游依赖。通过审查但尚未成功集成的任务不能提前解锁实现依赖。

## 8. 状态机与可靠性

### 8.1 逻辑任务状态

~~~text
draft → queued → ready → running → verifying → awaiting_review
                                              ├→ changes_requested → ready（新 attempt）
                                              └→ accepted → integrating → integrated

任意非终态 → waiting_user / blocked / cancelled / superseded
执行或验证失败 → blocked 或 failed；由已授权恢复策略决定是否新 attempt
~~~

waiting_user 是等待用户回答；blocked 是已知前置条件不足；failed 是本次逻辑任务已按策略终止；uncertain 用于执行尝试或集成操作结果未知。UI 明确区分，不能把所有情况都显示成“失败”。

integrated、cancelled、superseded、failed 为逻辑终态。Attempt 有自己独立的 running / succeeded / failed / cancelled / uncertain / revoked 终态；返工新建 attempt，旧结果只读。用户要求重试已终止逻辑任务时创建关联的新任务，不覆盖历史。

### 8.2 原子派工与迟到结果

每次领取在事务内检查依赖、资源与代际，创建 attempt_id、lease_epoch、到期时间，并写事件。启动执行前再次核对许可。执行许可含 project_run_epoch、task_epoch；所有结果携带 task_id、attempt_id、lease_epoch、plan_version、contract_revision。方案变更按 4.2 的兼容绑定判断，不能混用项目整体代际和任务代际。

撤销、换人、需求变更或暂停时增加代际并停止旧执行。旧任务即使稍后回复，也只能归档，不能完成当前任务、修改权威状态或进入集成。

### 8.3 事件与恢复

SQLite 为状态真相；状态更新和 outbox 事件同事务写入。单调事件序号供 SSE 游标重放。重复消息和工具操作用 idempotency_key 去重，唯一约束防止重复派工和重复创建修复轮次。

服务重启先取得实例锁，停止派工，核对存活执行器、租约、成果和集成日志。只对可证明未开始或可幂等的操作重试；结果未知的模型调用和外部动作标 uncertain，不自动重复收费或重写文件。核对完成后恢复独立任务。

### 8.4 默认恢复策略

- 瞬态超时/过载：有界退避；有服务端等待提示时遵循它。
- 限流与额度耗尽分开：只对可恢复的限流自动等待。
- 凭证过期：由认证管理者续期；需要重新登录时报告连接状态。
- 内容验收失败：形成问题清单和新 attempt。
- 主 Agent 服务不可用：保存项目依据，按用户预设备用策略重建主会话；无预设时保留状态并说明。
- 停止项目：撤销租约、终止执行并保存记录；恢复操作不能复活旧租约。

## 9. 数据合同

数据库核心表：projects、documents、conversations、messages、plan_versions、plan_confirmations、plan_adoptions、decisions、change_requests、roles、connections、connection_models、account_resources、credential_owners、agent_sessions、tasks、task_contracts、task_dependencies、attempts、artifacts、verifications、reviews、findings、suggestions、clarifications、usage_events、budget_reservations、resource_leases、integration_operations、events。

关键不可变内容保存摘要和创建来源；原始日志与大成果存文件系统，表内存引用。外键、schema 迁移和唯一约束必须启用并验证。凭证不进入任务 JSON、消息或 events。

### 9.1 示例任务合同

~~~json
{
  "task_id": "TASK-LOGIN-UI",
  "project_id": "PROJECT-1",
  "plan_version": 1,
  "plan_hash": "sha256:example-plan",
  "contract_revision": 1,
  "requirement_ids": ["REQ-LOGIN-01"],
  "basis_ids": ["AC-LOGIN-01", "IF-LOGIN-V1"],
  "role_id": "frontend-engineer",
  "objective": "按已确认接口实现登录页面",
  "base_commit": "example-base-commit",
  "depends_on": ["TASK-LOGIN-CONTRACT"],
  "read_paths": ["frontend/", "contracts/"],
  "write_dirs": ["frontend/src/pages/login/", "frontend/tests/login/"],
  "patch_only_paths": [],
  "resource_keys": [],
  "non_goals": ["新增人工审批", "修改登录接口", "更换前端框架"],
  "acceptance_ids": ["AC-LOGIN-01", "AC-LOGIN-02"],
  "verification_profile": "frontend-login",
  "deliverables": ["source_diff", "verification_evidence"],
  "policy_id": "task-write-scoped-v1",
  "max_review_rounds": 3
}
~~~

verification_profile 引用方案确认时固定的 runner、环境、正式测试包与配置摘要及验收映射，并按 7.4 在候选源码之外执行；改测试命令或测试基准需要合同修订和权限校验。Agent 不能通过删候选项目测试或改项目脚本替换正式验收。

### 9.2 正式审查与建议

~~~json
{
  "review_id": "REVIEW-2",
  "task_id": "TASK-LOGIN-UI",
  "attempt_id": "ATTEMPT-2",
  "plan_version": 1,
  "contract_revision": 1,
  "artifact_id": "ARTIFACT-2",
  "artifact_hash": "sha256:example-artifact",
  "verdict": "needs_revision",
  "findings": [{
    "id": "F-LOGIN-01",
    "basis_id": "AC-LOGIN-02",
    "severity": "blocking",
    "problem": "接口超时后页面一直显示加载状态",
    "evidence_ref": "VERIFY-2:case-login-timeout",
    "required_fix": "按原错误处理约定恢复可操作状态并显示错误",
    "recheck": "case-login-timeout"
  }],
  "suggestions": [{
    "id": "S-LOGIN-01",
    "text": "未来可讨论是否增加记住账号功能",
    "blocks_delivery": false
  }]
}
~~~

正式结论为 pass / needs_revision / blocked。blocked 必须关联已记录的缺失证据或澄清事项；不得含糊地当作增加要求。评分可以展示，不作为机器放行依据。

问题生命周期：open → claimed_fixed → verified_resolved；实现者只能声明已修复，审查方或对应验证才能关闭。无未解决阻塞问题且验收证据齐全才允许 pass。已通过条款不能被建议重新打开。

## 10. 平台工具与 API

### 10.1 主 Agent 工具

read_project、read_plan、draft_plan、propose_change、list_roles、list_connections_summary、create_task、revise_task_contract、dispatch_task、read_artifact、read_verification、submit_review、answer_worker_question、ask_user、record_suggestion、get_status。

revise_task_contract 只能调整既有方案内的分工和执行细节；撤销旧执行后再生效。改变产品要求必须由确认的新版本授权。Agent 工具无 confirm_plan、approve_change、原项目写入、凭证读取或随意执行宿主命令能力。

### 10.2 子 Agent 工具

read_context、受范围限制的读写工具、run_command_in_sandbox、submit_artifact、report_blocker、ask_coordinator、respond_to_findings。工具令牌绑定项目、任务、attempt 和代际；不提供派工、用户确认、账号管理或集成权限。

### 10.3 HTTP 接口草案

| 接口 | 使用者与行为 |
| --- | --- |
| POST /api/projects | 用户创建/导入项目 |
| POST /api/projects/{id}/messages | 用户与主 Agent 讨论，支持明确确认意图 |
| GET /api/projects/{id}/plans/{version} | 查看不可变版本 |
| POST /api/projects/{id}/plans/{version}/confirm | 用户身份确认指定哈希，带 expected_revision |
| POST /api/projects/{id}/changes | 主 Agent 提出变更或用户提出需求 |
| POST /api/changes/{id}/confirm | 用户确认具体变更摘要 |
| POST /api/clarifications/{id}/answer | 用户回答指定问题，带问题修订 |
| GET /api/projects/{id}/tasks | 任务、轮次与依赖投影 |
| GET /api/tasks/{id}/artifacts | 实际差异、验证和审查 |
| POST /api/projects/{id}/pause | 用户停止新派工并撤销在途执行 |
| POST /api/projects/{id}/resume | 用户恢复，生成新执行代际 |
| GET/POST/PATCH /api/roles | 用户维护角色模板 |
| GET/POST/PATCH /api/connections | 用户管理连接与非敏感配置 |
| POST /api/connections/{id}/auth/start | 启动该适配器允许的官方认证 |
| POST /api/connections/{id}/auth/complete | 完成带 state 校验的认证 |
| POST /api/connections/{id}/test | 显式连接/能力检测 |
| GET /api/projects/{id}/events | SSE，按 cursor 重放 |

状态变更接口支持 idempotency_key 和乐观版本检查，陈旧更新返回冲突。用户 API 与 Agent 工具使用不同认证主体；不能靠一个布尔字段区分人和 Agent。本机服务默认只监听回环，用户入口校验 Origin 与会话授权，任务容器拿不到用户会话凭据。

## 11. 界面信息结构

1. 项目页：持续对话、项目资料、当前方案与版本。
2. 方案页：需求、非目标、流程、验收、任务图与变更差异；确认操作显示准确版本。
3. 执行页：任务看板/依赖图、角色、连接、状态、负责目录和阻碍。
4. 任务详情：合同、执行活动、真实差异、验证证据、正式问题、修改轮次；建议独立显示。
5. 待确认：主 Agent 汇总的问题、选项、影响与回答历史。
6. 角色与连接：提示词、工具、模型、认证状态、出口、限额、计费来源及备用策略。
7. 交付页：需求覆盖、集成分支、组合验证、已知限制与成本。

用户可以查看子 Agent 全过程并请求主 Agent 干预。直接向执行者发送指令必须进入主 Agent 可见的记录；可能改变任务合同的内容先由主 Agent协调，避免两个互相矛盾的指挥来源。

## 12. 本地项目、资料与上下文

新项目在管理区创建仓库；已有项目建立独立导入副本，保留原仓库不动。未提交源码通过独立快照纳入，不自动 stash、reset 或提交用户原仓库。排除密钥、运行数据等时保存导入清单，缺失必要输入时明确报告。

首版资料支持 Markdown、文本和可提取文本的 PDF。每份资料保存来源和内容摘要，方案引用具体版本；扫描 PDF、图片、复杂 Office 文件标为需要额外解析，不假装已读懂。文档更新不会自动覆盖已确认需求。

上下文组装分为：固定平台规则、已确认方案、角色规则、任务合同、相关源码、上轮问题与验证、近期对话摘要。项目文件、外部文档和模型输出是数据，不能提升为平台权限或用户授权。压缩后仍重载固定方案版本和未解决问题，摘要不能成为新的需求依据。

## 13. 参考与证据边界

核对日期为 2026-10-22。以下链接支持接入与技术事实；本文其余条款为结合用户要求提出的工作台设计。

- [agentbox 账号与模型](../agentbox/docs/accounts-and-models.md)、[凭证服务](../agentbox/internal/credentials/service.go)、[账号切换](../agentbox/internal/workspace/account.go)、[网络配置](../agentbox/docs/networking.md)。本次为静态代码审阅。
- [agentbox 主管委派](../agentbox/docs/architecture/delegation-supervisor.md)：当前委派与反馈机制可参考，完整自动验收闭环需自行实现。
- [dsh-agent-teams](https://github.com/NanmiCoder/dsh-agent-teams) 与 [质量门禁文档](https://github.com/NanmiCoder/dsh-agent-teams/blob/main/docs/quality-gates.md)：参考任务依赖、显式审查和修复轮次思路；未运行验证。
- [OpenAI 官方认证文档](https://learn.chatgpt.com/docs/auth)、[Codex App Server](https://learn.chatgpt.com/docs/app-server)：账号与应用集成入口。
- [Sign in with ChatGPT](https://developers.openai.com/siwc/token-sharing-open-source)、[预览限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)：本地/开源应用的适用条件和限制，实施时重新核对。
- [Claude 官方认证](https://code.claude.com/docs/en/authentication)、[Agent SDK 概览](https://code.claude.com/docs/en/agent-sdk/overview)、[第三方使用说明](https://support.claude.com/en/articles/13189465-log-in-to-your-claude-account)：原生工具登录与第三方产品接入边界。
- [Git worktree](https://git-scm.com/docs/git-worktree)：任务版本副本；[Docker 运行约束](https://docs.docker.com/engine/containers/run/)、[只读挂载](https://docs.docker.com/engine/storage/bind-mounts/)：隔离执行基础。组合策略必须通过本方案测试，文档本身不等于实现证据。
