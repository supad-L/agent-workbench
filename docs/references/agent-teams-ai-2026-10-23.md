# Agent Teams AI：接入与运行交互源码研究

日期：2026-10-23，Asia/Shanghai。依据用户本日指定的参考项目，研究多平台接入、Agent 卡片和可视化运行交互。本文是来源核对和实施建议，不替换已确认四份方案，不将参考项目的功能列表自动变成本项目的新增验收条件。

## 研究版本与证据

- 来源：[777genius/agent-teams-ai](https://github.com/777genius/agent-teams-ai)。
- 独立源码：`/Volumes/D/supad/agent-teams-ai`；按用户要求放在 supad 根目录，与工作台仓库分开。
- 固定提交：`6573f3253877e9cef700eece281ca7153d650422`；package.json 版本为 `2.17.10`。以下源码链接固定到该提交，避免后续 main 变化使研究失去依据。
- 已阅读关键源码、部分相关测试及仓库内界面截图。没有安装/启动参考应用，也没有用它检测、读取或登录本机模型账号；运行稳定性和真实服务商兼容性未在本轮实测。
- 参考仓库按原许可证保留。本轮仅记录源码位置和设计分析，没有将其代码、头像或截图复制到工作台实现中。

## 1. 多平台接入：先选使用方式，再完成操作

### 已在源码中确认

快速接入使用提供方卡片，每张卡片有名称、说明、当前状态和对应操作。运行时的检查/缺失/安装/就绪状态与提供方的可连接/已连接/认证方式不同/需要升级/手动配置状态分开表达（S3、S4）。这能直接回答“现在缺什么、下一步点哪里”。

引导页使用四个明确阶段：`connect → verifying → choose-model → ready`，另有错误状态；有计划选择、认证类型、连接策略、当前进度和已选择模型的结构化数据（S1、S2、S19）。重新连接时保留原认证方式，API 凭证替换与 OAuth 重新授权使用不同文案（S5）。

它的接入不是一个统一 OAuth 函数覆盖所有平台：上层区分原生 Anthropic/Codex/Gemini/OpenCode 等运行时能力，快速提供方目录通过 `runtimeId: opencode` 加载，多家渠道的认证和模型列表复用 OpenCode 体系；部分渠道还经过 companion/plugin（S2、S6、S8）。因此不能把其提供方数量理解成“每家都由本应用直接适配并完成实测”。

模型列表、账号已认证、目录是否过期、检查是否为当前有效证据分别判断；缓存目录不会自动变成可执行依据。项目切换时立即隔离旧项目目录结果，请求序号防止晚到响应覆盖当前页面（S6、S7）。账号控制面还单独清理不适用的认证/路由环境，执行时根据选定认证方式构建环境（S9、S10）。

### 对工作台的借鉴

| 当前工作台 | 可借鉴的交互 | 依赖与边界 |
| --- | --- | --- |
| “模型连接”和“订阅账号”入口分开，用户需先理解协议 | 增加接入方式入口：自定义 API / Coding Plan、GPT 官方账号、Claude 官方账号；进入各自流程 | 是对已有渠道的引导，不自动承诺新的订阅渠道 |
| API 页一次展示地址、模型、认证、网络和高级配置 | 用“选择方式 → 配置 → 显式验证 → 绑定角色”的导航组织内容，高级设置保留 | 保存和格式检查仍不调用模型；模型测试由用户显式触发并继续记账 |
| 已保存、格式检查和工具测试有各自结果，跨页面不集中 | 在连接摘要展示已有证据和对应操作 | 格式有效、已授权、模型可见、工具验证、隔离可执行需要分别显示，不能一个绿点混用 |
| 原生账号未完成真实执行验收 | 引导页直接呈现缺失条件和下一步；登录状态与执行就绪分开 | GPT 仍走官方接口和工作台独立目录；Claude 在强制出口验收前不开放登录 |
| 用户手工配置真实中转和模型 | 先用少量可维护的入口帮助理解，保留完整自定义表单 | 不按模型名称猜能力、价格和授权；不从参考仓库抄固定模型名单作为已验证目录 |

值得采用的是“清晰步骤、对应动作、证据分级”。其自动验证或尝试多个模型的行为不能直接移植；工作台仍按用户显式测试、预算和既定备用策略执行。

## 2. Agent 卡片：把配置身份与正在做的事连起来

### 已在源码中确认

`MemberCard` 是实时团队成员入口，包含头像/颜色、名称、角色、模型/运行时摘要、启动/存活/等待状态、当前开发或审查任务、任务计数、快捷操作及可选资源遥测（S11）。卡片支持键盘进入；当前任务可单独打开，发消息、分配任务、编辑成员等操作有独立回调。

卡片没有用一个 `enabled` 字段代表真实在线：启动信息、运行实例、任务状态和团队状态共同决定展示；缺少存活证据时会隐藏不适用的当前任务。资源小图使用实际样本，而不是单纯装饰动画（S11）。

### 对工作台的借鉴

工作台现有“Agent 角色”页是可复用模板配置；参考项目成员卡片对应项目中的运行实例。两者应通过稳定角色/运行编号关联，保留各自职责。

- **角色配置卡片**：保留已确认的正常窗口每行三张及窄屏两列/一列；继续展示职责、模型、工具范围和配置操作。启用状态明确叫“已启用”，不显示成“在线”。
- **项目运行成员卡片**：关联实际 run/attempt，展示当前工作、最近事件、开始/更新时间、模型和运行状态；点击进入该次运行详情。没有运行数据时呈现空态，不补造在线状态。
- **快捷操作**：M2 只放已有的试运行、查看记录、取消及显式继续；派工、成员互发消息、审查入口随相应服务接通。按钮直接对应权限和后台动作。
- **摘要与详情**：卡片只放当前摘要，完整提示词、输出、历史、用量和证据进入抽屉/详情，避免三列卡片膨胀为日志页。
- **识别性**：角色图标、稳定颜色、状态文字一起表达身份和状态；机器人头像可作为另行设计的视觉方向，不是本轮新增强制要求。

## 3. 可视化运行页面：图与列表共享同一套任务和操作

### 已在源码中确认

关系图不是静态拓扑截图。`TeamGraphAdapter` 将团队、成员、任务、消息、进程、工具活动等业务数据投影到图节点、边和活动展示（S12）。`GraphEventPort` 区分单击节点、双击进入详情、点击边、打开任务、打开成员和发送消息；页面将这些事件接回同一组任务/成员详情和操作（S13–S15）。

实现分成三部分：独立 `packages/agent-graph` 负责画布和底层交互，业务适配器负责数据含义，React 覆盖层负责任务卡、成员详情、日志预览、阻塞说明等可交互内容（S12、S13）。这让图中点击与列表中点击能进入同一份详情，而不用维护两个任务状态机。

缩小时显示概况、放大后增加任务细节；显示项还受当前焦点影响（S16）。日志预览按成员和运行 lane 区分，只拉有限条目，有缓存和去抖（S17）。页面支持停用视图暂停动画（S13、S20）。稳定槽位的设计记录专门区分布局位置、业务更新与镜头状态（S18）；该文档中的全部计划不能仅凭存在就认定已实现。

### 对工作台的借鉴

建议让项目运行页具有三个协同区域：

```text
项目、确认方案版本、运行状态、停止/继续
┌──────────────────┬─────────────────────────┬──────────────────┐
│ Agent / 运行列表 │ 卡片视图、任务看板或关系图 │ 选中项详情       │
│ 当前工作与状态   │ 成员 / 任务 / 依赖 / 阻塞 │ 输出、工具、证据 │
│ 需要关注的项目   │ 真实活动事件驱动的变化     │ 历史、用量、原因 │
└──────────────────┴─────────────────────────┴──────────────────┘
```

这里的三部分是页面信息组织建议，不要求同时在小窗口全部展开；详情可用抽屉承载。

- 单击 Agent 看本次运行，单击任务看合同/attempt/成果；点击依赖边说明“被什么阻塞”，并能进入对应任务。
- 列表、看板、图共享稳定 ID、状态快照和详情组件；视图切换只改变展示，不改变任务状态或执行权限。
- 文本和工具活动来自后端事件，进入详情仍从服务读取当前快照。事件重连不重复派工、计费或播放整段历史为“正在执行”。
- 选中/放大保持上下文；日志到达时不重排全部卡片或重置镜头。保存布局使用稳定成员/运行身份，不用显示名。
- M3 才有正式任务/依赖/调度/证据数据；M4 才有审查与集成状态。图中不能把 CLI 成功或用户拖动卡片直接变成正式审查通过。
- CPU/内存、复杂动画和多组织地图是后续可选增强，不增加当前 M2 的阻塞验收条件。已确认的主 Agent 与角色工作流继续由平台状态机约束。

## 4. 落地顺序与现有代码映射

| 顺序 | 适用阶段 | 可独立实施的工作 | 工作台入口 | 完成时验证 |
| --- | --- | --- | --- | --- |
| 1 | M2 | 接入方式导航与分步状态，明确已保存/已授权/已验证/未就绪 | `web/src/connections.tsx`、`accounts.tsx`、`desktop/native-accounts.cjs` | 各入口正确，保存不计费，测试仅显式发起，未就绪原因可见 |
| 2 | M2 | 当前只读试运行的可追踪详情：模型、请求/工具进度、输出、历史、取消/继续 | `web/src/role-trial.tsx`、`internal/store/runs.go`、`runservice/manager.go` | 以真实本地合成服务验证事件；快速返回、历史切换和晚到结果不串项目/运行 |
| 3 | M3 | 项目运行成员卡片和任务看板，关联 role/run/attempt | 依既定任务、调度、工具和证据服务实施 | 显示源于真实任务状态；停止、租约和账号槽一致 |
| 4 | M3–M5 | 共享数据投影的关系图，节点/边到详情联动，稳定布局 | 独立业务投影和图交互层，复用既有详情 | 图与列表一致；重连不重执行；历史不伪装实时；大图和窄屏交互验证 |
| 5 | M4–M5 | 审查、返工、证据、串行集成的可视交互 | 依既定审查和集成服务 | 方案版本、合同、attempt 和成果引用一致，展示不能绕过正式审查 |

其中顺序 1、2 能改善 M2 的可用性；它们不能替代原生授权、账号交接、持久会话恢复和 Claude 全链路强制出口。M2 未验收项继续按 [M2 记录](../m2-report.md) 与 [原生执行器记录](../m2-native-runtime.md) 推进。

## 5. 源码导航（固定提交）

- **S1** [多平台接入引导视图](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/renderer/ui/RuntimeProviderOnboardingView.tsx)：`src/features/runtime-provider-management/renderer/ui/RuntimeProviderOnboardingView.tsx`。
- **S2** [引导状态与进度合同](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/core/domain/runtimeProviderOnboarding.ts)：`src/features/runtime-provider-management/core/domain/runtimeProviderOnboarding.ts`。
- **S3** [快速接入卡片与前置条件](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/renderer/ui/RuntimeProviderQuickConnectView.tsx)：`src/features/runtime-provider-management/renderer/ui/RuntimeProviderQuickConnectView.tsx`。
- **S4** [接入状态判断](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/core/domain/runtimeProviderQuickConnect.ts)：`src/features/runtime-provider-management/core/domain/runtimeProviderQuickConnect.ts`。
- **S5** [切换/重新连接的认证意图](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/core/domain/runtimeProviderConnection.ts)：`src/features/runtime-provider-management/core/domain/runtimeProviderConnection.ts`。
- **S6** [项目范围与请求代际保护](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/renderer/hooks/useRuntimeProviderQuickConnect.ts)：`src/features/runtime-provider-management/renderer/hooks/useRuntimeProviderQuickConnect.ts`。
- **S7** [提供方状态与模型目录证据](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/shared/utils/providerStatusAuthority.ts)：`src/shared/utils/providerStatusAuthority.ts`。
- **S8** [原生工具及提供方配置适配](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/main/services/runtime/ProviderConnectionService.ts)：`src/main/services/runtime/ProviderConnectionService.ts`。
- **S9** [Codex 官方账号接口封装](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/codex-account/main/infrastructure/CodexAccountAppServerClient.ts)：`src/features/codex-account/main/infrastructure/CodexAccountAppServerClient.ts`。
- **S10** [账号环境与执行认证选择](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/codex-account/main/infrastructure/CodexAccountEnvBuilder.ts)：`src/features/codex-account/main/infrastructure/CodexAccountEnvBuilder.ts`。
- **S11** [Agent 成员卡片](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/renderer/components/team/members/MemberCard.tsx)：`src/renderer/components/team/members/MemberCard.tsx`。
- **S12** [业务状态到关系图的投影](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/agent-graph/renderer/adapters/TeamGraphAdapter.ts)：`src/features/agent-graph/renderer/adapters/TeamGraphAdapter.ts`。
- **S13** [可视化页面与覆盖层](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/agent-graph/renderer/ui/TeamGraphTab.tsx)：`src/features/agent-graph/renderer/ui/TeamGraphTab.tsx`。
- **S14** [图节点、任务和成员交互复用](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/agent-graph/renderer/hooks/useGraphSurfaceInteractions.tsx)：`src/features/agent-graph/renderer/hooks/useGraphSurfaceInteractions.tsx`。
- **S15** [关系图交互端口](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/packages/agent-graph/src/ports/GraphEventPort.ts)：`packages/agent-graph/src/ports/GraphEventPort.ts`。
- **S16** [按缩放级别显示内容](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/packages/agent-graph/src/canvas/semantic-zoom.ts)：`packages/agent-graph/src/canvas/semantic-zoom.ts`。
- **S17** [日志预览与批量缓存](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/agent-graph/renderer/hooks/useGraphMemberLogPreviews.ts)：`src/features/agent-graph/renderer/hooks/useGraphMemberLogPreviews.ts`。
- **S18** [图布局稳定性设计记录](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/agent-graph/STABLE_SLOT_LAYOUT_PLAN.md)：`src/features/agent-graph/STABLE_SLOT_LAYOUT_PLAN.md`。
- **S19** [接入引导逻辑与模型验证](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/src/features/runtime-provider-management/renderer/hooks/useRuntimeProviderOnboarding.ts)：`src/features/runtime-provider-management/renderer/hooks/useRuntimeProviderOnboarding.ts`。
- **S20** [图画布与动画控制](https://github.com/777genius/agent-teams-ai/blob/6573f3253877e9cef700eece281ca7153d650422/packages/agent-graph/src/ui/GraphView.tsx)：`packages/agent-graph/src/ui/GraphView.tsx`。

已抽查的测试入口：

- 接入认证意图：`src/features/runtime-provider-management/core/domain/runtimeProviderConnection.test.ts`。
- 图业务投影：`test/renderer/features/agent-graph/TeamGraphAdapter.test.ts`。
- 语义缩放：`test/renderer/features/agent-graph/semanticZoom.test.ts`。
- 稳定位置：`test/agent-graph/stableSlots.test.ts`。

视觉核对使用参考仓库内 `docs/screenshots/4.png`、`1.png`、`organization-map.png`。这些是仓库静态截图，可能与该提交的具体页面细节存在时间差；本轮没有将其当作实际运行验收证据。
