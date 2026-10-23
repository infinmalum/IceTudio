# P0 执行契约：删除边界、替代前置与保留能力

状态：2026-10-16 制定，2026-10-17 P0 已按实施计划执行。本文保留操作前置条件及不可退化约束；旧符号路径是重构前定位，不表示当前仍存在这些产品或模块。本文是 [实施计划](../IMPLEMENTATION_PLAN.md) 的规范性组成部分；不是建议清单。当前产品语义见[架构](architecture.md)。本次更正取消早先“首版单一 Agent、清理专属调度、删除 planner/worker 核心”的含义。

## 0. 阅读顺序与操作语义

实施前读取：AGENTS.md → 本文及[全表/接口清单](p0-api-storage-inventory.md) → 实施计划 → 架构。用户新指令优先；当前文档互相冲突时，保留有争议的能力并修正文档，禁止选择更宽的删除解释。历史文档、旧实现参考和聊天中的文件清单都不构成额外删除授权。

| 操作 | 允许做什么 | 不允许推导什么 |
|---|---|---|
| DELETE | 删除明确列出的产品契约、符号或分支及其专属测试 | 不能连同共享文件、通用框架、存储关系一起删 |
| MOVE | 将入口移到任务详情/统一设置/开发者设置，继续可访问与工作 | 不代表禁用、取消 API 或“以后重建” |
| REPLACE | 完成目标载体、接线和等价验收后移除旧载体 | 不代表先删除运行能力，靠新代码空壳通过构建 |
| KEEP | 保留当前能力与测试语义；可适配配置类型、品牌与工程绑定 | 不代表文件逐字不动，也不允许通过永远关闭能力来声称保留 |

**没有明确列为 DELETE 的现有能力默认 KEEP。删除白名单以功能/符号为单位，文件路径只是定位信息。** 下文若列出混合文件，不能将整文件标记为删除。无实际用户/不兼容旧格式的政策只影响历史读取迁移，不影响保留当前执行能力。

## 1. 产品入口与内部运行的固定边界

用户只需一个创作助手入口。内部必须保留：

- main、planner、worker、reviewer、improver 五种角色、独立模型/指令/工具子集与运行实例。
- direct（含同步委派）、dynamic（规划与宿主调度）、fixed（固定阶段及依赖）三种当前已实现模式。
- worker 槽位与独立配置、根子运行关联、只读并行、写入/未知副作用串行、计划修订、任务原子领取和交付证据。
- 独立审查、required 完成门槛、当前已有有限返工、按需复核、独立复盘及 worker 轨迹。
- 根预算/用量预留与退款、未知用量、父子取消、重启恢复、fixed 等待输入与显式续跑、未知效果禁止盲目重放。
- 会话待办计划和真正调度任务图各自的语义、存储、查询和测试。会话计划不等于执行完成证明。
- 同根任务必要的过程上下文和证据传递。这不属于要删除的跨对话长期记忆。

默认 direct 是默认值，不是能力删除。P0 后必须能通过新配置入口启动三种模式；只有 dead code、只能手改数据库或只有测试才能调用，均不满足 KEEP。未实现的条件分支、固定流程自动返工、动态同根续跑等不因本次纠正自动纳入 P0。

## 2. DELETE 白名单与共享例外

### D1：四套通用场景模板

- UI：删除 `ProfileTemplates.tsx` 的四场景选择/创建产品及其接线。
- 服务/存储：删除 `CreateTemplateProfile`、`RestoreTemplateProfile` 等专属路径、`ProfileTemplates()`、`ProfileTemplateByID()` 与 daily/writing/research/development 内容；删除仅用于这些模板的种子记录。
- IPC/CLI/Wails：删除 `templates.*`、CLI templates 组和相应绑定。
- 测试：删除四套模板内容/创建的专属断言；模板中复用的 direct/dynamic/reviewer 行为测试改用 RuntimeConfig fixture，不能删除行为覆盖。
- **禁止整删** `internal/storage/execution.go` 或 `conversations.go`：前者含 Execution、WorkerConfiguration、ComponentConfig、Components、解析校验，后者含保留的会话管理。新默认 RuntimeConfig 与默认创作指令必须存在，不用旧 daily 模板充当新默认。

### D2：Profile 管理、分享、生成产品

- UI：删除 Profile 库的 list/create/copy/restore/archive、Profile ZIP 导入导出、选择 Profile 开对话、生成/改进 Profile 的入口。定位 `ProfileList/ProfileEditor/ProfileImport/ProfileDrafts/ConversationProfile.tsx`、`useProfiles.ts`、`useProfileDrafts.ts`；其中共享能力先提取。
- 服务：删除 Profile 专属 CRUD、引用替换/归档、分享包解析/导出、Profile 草案生成/采用/恢复。定位 `app/profiles.go`、`profile_drafts.go`、`share.go` 中的专属符号；校验、范围继承、Skill 资源及通用工具函数按实际引用提取保留。
- 存储：`profile_drafts` 删除；`profiles/profile_revisions/profile_revision_scopes` 及 Profile 外键属于 R1 的 REPLACE，不允许先删。Profile 专属引用/归档函数在 worker 配置来源替换后删除。
- IPC：`profiles.*`、`drafts.*` 和 `conversations.select` 删除。`components.list` **保留**，它查询的是程序角色注册，不是 Profile CRUD。
- CLI：删除 profiles 组及 conversations select、create 的 `--profile` 参数；新建对话自动使用 RuntimeConfig。`--reuse` 中复用旧对话记忆/上下文的产品路径删除，委派子会话仍受根任务控制。
- **明确 KEEP**：`app/draft_model.go` 的 `callDraftModel`、`app/draft_evidence.go` 的 `inheritDraftEvidence`、`storage/draft_evidence.go` 及 `draft_evidence` 表、`StartImprover`/improver 角色、Skill 草案的全部服务/存储/CLI/IPC。它们当前确实由 `app/skill_drafts.go` 调用，不能随 Profile 草案删除。
- **明确 KEEP**：`TaskEvidence.tsx` 当前是工作记录选择器，被 `SkillDrafts.tsx` 使用；不是可随任务图删除的展示件。
- Profile 分享包删除不禁止独立 Skill 内容及合法资源的管理/未来分享；P0 不用另造 Profile 格式继续保留旧产品。

### D3：独立长期记忆

- 删除 `context_memories` 和其专属索引、长期记忆 CRUD/导出、`local__memory_save`、回忆注入、`MemoryEnabled` 与 ContextPolicy 中的记忆开关。
- 删除 `MemoryPanel.tsx`；`PrivacySettings.tsx` 只删记忆部分，授权、外发与隐私相关其余功能保留。
- 删除 CLI memory 组、IPC `memory.*` 和 List/Add/Update/Delete/ExportMemory 对应绑定。
- `app/memory.go` 是混合文件：删除 ListMemories/AddMemory/UpdateMemory/DeleteMemory/ExportMemories；**保留并移出** GetSessionSummary、SessionPlanView、GetSessionPlan、ClearSessionPlan、checkSession。
- `app/history.go` 只删除长期记忆召回分支/常量/专属检查；保留历史组装、会话摘要、会话计划、材料与来源范围。`internal/agent` 的压缩/上下文循环不删。
- 不删除 contexts、ContextPolicy 的非记忆字段、exfil_grants、messages、summaries、session_plans、task_*、worker_observations、Skill 证据和运行快照。

### D4：仅删除或迁移旧配置 UI 外壳，不删其能力

- `ProfileAdvanced/ProfileExecution/ProfileStages/ProfileCapabilities.tsx` 的 Profile 表单接线替换为新配置接线；角色、worker、流程、阶段、能力、模型覆盖、审查与预算设置 **MOVE 到唯一开发者设置**。MCP/Skills 安装与常用启用留在统一设置，不强迫普通用户进入开发者页。
- `TaskProgress.tsx` **KEEP/MOVE**：保留任务图、会话计划、审查、复盘和 worker 轨迹，收纳进任务详情。仅“改进助手/Profile”的回调替换为 Skill 反馈入口。
- “独立复盘产品”删除范围现在限定为不再建设独立首页/导航产品；已有复盘服务、详情按钮、证据、CLI/IPC 均 KEEP，不是删除 retrospective 功能。

以上 D1–D4 是 P0 删除边界；没有“其他看起来通用就可以删除”的条款。

## 3. R1：Profile 配置载体替换，先完成再删除

目标内部类型称为 `RuntimeConfig`（应用执行配置）、`RuntimeConfigRevision`（不可变修订）、`RunConfigSnapshot`（运行冻结配置）。可按项目命名风格落实具体类型，但以下语义是强制要求。

| 原职责 | 替代要求 |
|---|---|
| ProfileData 的模型/指令/能力与 contextBudget | RuntimeConfig 保存默认模型引用、指令、MCP/Skills 选择、工具范围与上下文预算；凭据仍由本机凭据服务解析 |
| Execution、ComponentConfig、Components、ResolveComponent | 保留并适配 RuntimeConfig；五角色配置、版本和模型/工具子集可解析和校验 |
| workflow/stages、review、budget、delegation | 完整保留当前 direct/dynamic/fixed、固定阶段输入、审查策略、根预算和委派上限；不强制全改 direct |
| WorkerSource.ProfileID / WorkerConfiguration | 移除对另一 Profile 的引用；使用显式 worker 配置（模型、指令、能力等），稳定槽位名继续存在；不删除 worker 来源配置能力 |
| Profile 修订和来源 scopes | 新执行配置保存修订及来源范围；多字段更新原子发布；所有调用者使用一致修订 |
| RunProfile / FrozenProfile / RunResources | 适配为运行配置快照，仍冻结 root/planner/reviewer/workers、Skill 内容及资源、范围、搜索和权限上限 |
| sessions.profile_id / runs.profile_* | 移除旧产品引用；新运行记录实际 RuntimeConfig 修订及内容，不能只存一个会变的配置指针 |
| SkillReferences 与配置验证 | 查询新能力选择/配置引用，继续禁止撤销仍被引用的资源，保留来源敏感度传播 |

P0 提供一套自动创建的应用默认执行配置，包含可实际使用的角色/worker 配置。普通用户无需建立配置库。后续工程级覆盖属于 P1–P3，不得用“暂时没有工程”跳过 P0 配置接线。

必须提供同一服务的读取/更新接口及开发者设置入口，保存时执行角色/阶段/预算/资源校验与预期修订比较。Wails/IPC 暴露等价配置读写，CLI 提供内部配置读取/更新路径（命名在实现提交中统一记录）。它们不是继续保留 profiles CRUD 的别名。三种模式须能通过这个入口选择并启动。

替换顺序固定：新增载体与服务 → 接通角色解析/冻结/技能引用/来源范围 → 接通新设置与运行 → G1/G2/G5 验收通过 → 移除旧 CRUD/外键/表/模板种子与生成绑定。开发中间提交可暂时保留旧载体，但 P0 完成时不得有运行依赖 Profile，也不得以删除全部协作为捷径。

此处是新开发 schema 的代码重构，不要求迁移旧数据库。旧文件拒绝并保留。共享能力的重构不意味着数据源范围可丢弃。

## 4. KEEP 模块定位（禁止按这些文件整块删）

所有路径以仓库根目录为基准，列表是重点保护项，不是只有这些才保留。

| 层 | 重点路径 | 保留内容/允许修改 |
|---|---|---|
| Agent | `internal/agent/engine.go`、`runs.go`、`compaction.go` | 循环、StartWorker/StartPlannedWorker/StartPlanner/StartReviewer/StartImprover、ContinueFixed、父子取消；只替换配置载体和删除长期记忆专属接线 |
| 调度 | `internal/app/dynamic.go`、`delegation.go`、`task_graph.go`、`workers.go`、`worker_outcome.go`、`worker_traces.go` | 全部执行能力 KEEP，适配新配置和音乐资源 |
| 检查 | `internal/app/reviewer.go`、`rework.go`、`retrospective.go` | 独立审查、返工、复盘保留；音乐质量不因结构检查通过而自动通过 |
| 恢复/预算 | `internal/app/task_continuation.go`、`run_policy.go`、`recorder.go`、`internal/storage/task_continuation.go`、`coordination.go` | 游标、尝试、根预算、等待输入、重启与未知结果契约保留 |
| 存储执行 | `internal/storage/task_graph.go`、`execution.go`、`fixed_workflow.go`、`reviews.go`、`retrospectives.go`、`run_resources.go`、`worker_traces.go`、`plans.go` | 保留全部非 Profile 产品/模板专属语义；run_resources 的 worker 部分不能删除 |
| 共享草案 | `internal/app/draft_model.go`、`draft_evidence.go`；`internal/storage/draft_evidence.go`、`config_scopes.go` | 保留 Skill 需要的模型/证据/范围逻辑；profile-specific 来源查询替换后可删旧符号，不可丢掉范围信息 |
| 权限 | `internal/storage/profiles.go` 中 PermissionScope、DelegationPolicy、ContextPolicy 等；`policy.go`；`app/gate.go`、`approvals.go`、`exfil.go`、`send_consent.go` | 类型迁出混合文件；权限实体/字段与委派边界保留，记忆字段除外 |
| 扩展/材料 | `internal/model/`、`internal/mcptools/`、`internal/skills/`、`internal/materials/`、app 的 search/mcp/tool_discovery/basic_tools | 全链路 KEEP，必要适配，不宣称完全不改代码 |
| UI | TaskProgress、TaskEvidence、SkillDrafts、ConversationPanel、Transcript、授权/模型/扩展面板 | 保留实际能力，迁移入口及接线；不能只保留后端却删掉所需用户入口 |

## 5. 存储、服务与接口的范围

完整当前表和 IPC 路由逐项分类见[清单](p0-api-storage-inventory.md)。这份清单覆盖当前 `schemaSQL` 的 CREATE TABLE 与 IPC switch 路由；实施时新增或发现未覆盖的符号必须补清单，不套用通配删除。

特别约束：root_budgets、model_usage 都依赖 task_roots；必须一起保留，不能先 DROP task_roots 再说预算“以后另挂”。draft_evidence 支持 Skill；session_plans 是会话待办；task_continuations/task_inputs 支持等待和续跑；它们都不是模板或 Profile 库的附属垃圾。

P0 保留 contexts 和 exfil_grants，删除记忆字段不删除授权。父子运行共享根授权且只能收窄；与另一独立对话的历史不自动共享。工程授权在 P1 增加显式绑定，不在 P0 猜测改挂所有关系。

Wails 的 `StatusService`、`conversationservice.go` 都是混合门面。仅删除 D1/D2/D3 对应绑定；保留 ListComponents、GetTaskGraph、ListTaskReviews、StartConversationReview、StartConversationRetrospective、ListTaskRetrospectives、ContinueConversationTask、GetSessionPlan/ClearSessionPlan、ListChildRuns、ListWorkerTraces 及 SkillDraft 全套。重新生成绑定后核对调用方，不手删生成目录来掩盖服务缺失。

## 6. CLI 和工具

| KEEP / 适配 | DELETE |
|---|---|
| serve/status/help/version/logs/events | templates 命令组 |
| conversations list/create/start/show/search/permissions；create 改用 RuntimeConfig | profiles 命令组（含 Profile drafts/export/import）和 conversations select / --profile |
| **tasks status/cancel/continue/review/retrospect**，status 继续显示根子运行/审查/计划 | memory 命令组 |
| approvals、skill-drafts；新增配置读写接线 | --reuse 中旧对话记忆复用产品路径 |

工具保留会话计划更新、任务图编辑/执行、委派、过程查询/观察、Skill 加载、MCP 搜索与调用、联网搜索、文件/命令能力。只删除长期记忆工具。具体工具名以注册源为准，不能按 local__ 前缀整体删除。组件角色指令保留执行职责，改为音乐任务语境；不删除角色本身。

## 7. 分批顺序与完成门槛

每批必须保持可编译且保留路径有实际入口，不以删测试来通过。

1. 复核清单并建立当前行为基线；将混合模块的保留符号迁到合适文件。
2. 实现 RuntimeConfig、修订/范围、配置接口和角色/快照接线；新配置驱动所有保留模式。
3. 移动开发者设置与任务详情，迁移 Skill 证据及授权 UI 接线。
4. 仅删除 D1/D2/D3 专属符号，移除旧 Profile 表/引用前满足替代门槛。
5. 名称/绑定/构建及当前 schema 同步更新，执行以下验收并记录缺口。

| 门槛 | 必须有的证据 |
|---|---|
| G0 范围 | 每项删除对应 D 编号、精确符号/表/API；混合文件注明迁出目标与剩余调用者；不出现未授权删除 |
| G1 可达性 | 新配置读写接口与开发者 UI 可保存/加载；direct/dynamic/fixed 均能从新配置启动；无 Profile 必要依赖，无永久强制 direct |
| G2 协作 | 委派、根子关联、规划修订、原子领取、只读并行、写串行、阶段依赖和真实交付证据验证 |
| G3 审查 | required 门槛、缺陷返工次数/根预算、按需 review、retrospect 和证据失效验证；不能把 succeeded 当作 reviewed |
| G4 恢复 | 父子取消、重启不盲重放、未知效果、fixed 等待/输入绑定/续跑、剩余预算与时间验证 |
| G5 Skill | StartImprover 到 Skill 候选/采用/回退全链路；任务树证据捕获与来源范围继承、版本冻结、资源引用与发布原子性 |
| G6 记忆边界 | 长期记忆存储/工具/注入消失；会话历史、摘要、计划和同根过程共享仍存在，独立对话不串数据 |
| G7 安全扩展 | 模型/凭据、MCP schema/审批、Skills、搜索、材料、冻结/实时授权和外发限制回归；拒绝必要操作不误报成功 |
| G8 产品 | 无通用模板/Profile 库/分享/生成入口；普通使用无需组件配置；开发者配置及任务图/审查/复盘仍可操作；草稿与未保存更改保护 |
| G9 构建/持久化 | 当前 schema/关系/重启与原子性测试；适用 Go/race/UI/Wails 构建通过；旧格式拒绝且文件保留；品牌和生成绑定一致 |

`dynamic/fixed_workflow/workers/reviewer/rework/retrospective/task_graph/task_continuation/coordination/worker_traces/run_resources/plans/draft_evidence` 等测试族是 KEEP，不因删除旧配置表而整组删除。profile/share/scenes/memory 测试按断言拆分：废弃产品断言删除，涉及 retained contract 的权限、并发、冻结、原子性断言迁移到新 fixture。

未来音乐编辑另加：worker 与 main 共用音乐事务；不同 worker 与 GUI 的写冲突检查；授权范围/预算不能因委派扩大；审查绑定工程修订。P0 不要求伪造这些尚未实现的音乐功能。

## 8. 对上一份误读清单的明确纠正

- 删除 dynamic/delegation/workers/reviewer/rework/retrospective 核心：**禁止**。
- 删除 task_roots/task_items/task_attempts/task_*、reviews、worker_observations、session_plans：**禁止**。
- 删除 tasks review/retrospect、tasks.graph/reviews/retrospectives、runs.children、plans.get/clear、components.list：**禁止**。
- 删除 improver、draft_model、draft_evidence 表/模块、TaskEvidence：**禁止**。
- 整删 app/memory.go 或 execution.go：**禁止**；按 D3/D1 拆分。
- 把 LocalContext 不可见解释成删除 contexts 或改成全局授权：**禁止**。
- “其余条目不受影响”“每项全层删除”不是有效推断：Profile/Skill、记忆/计划、模板/执行注册均有共享依赖，必须按本契约处理。
