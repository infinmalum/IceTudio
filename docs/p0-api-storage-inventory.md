# P0 存储与 IPC 逐项清单

状态：2026-10-16，当前源代码清点；这是待实施范围，不是已完成重构。操作定义和替代门槛以 [P0 执行契约](p0-refactor-contract.md)为准。未列出的能力默认 KEEP，不能推导批量删除。

## 存储

覆盖 `internal/storage/schema.go` 当前 CREATE TABLE。KEEP 允许替换 Profile 外键和配置类型，禁止删除表所承载的行为。REPLACE 必须先完成 R1 并通过 G1/G2/G5；无旧库迁移要求不构成删除保留能力的授权。

| 表 | 操作 | 边界 |
|---|---|---|
| `context_memories` | 已删除（2026-10-17） | D3：独立长期记忆表、索引、CRUD、注入与 local__memory_save 一并移除；会话摘要/计划保留。 |
| `contexts` | KEEP | 授权实体保留；仅删除长期记忆字段，不改成全局授权。 |
| `draft_evidence` | KEEP | Skill 草案共享证据保留；移除 Profile 专属消费者。 |
| `exfil_grants` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `mcp_endpoints` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `message_files` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `messages` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `model_usage` | KEEP | 用量、预留及根任务关系保留。 |
| `profile_drafts` | DELETE | D2：仅 Profile 草案；不删除 Skill 草案与共享证据。 |
| `profile_revision_scopes` | REPLACE | R1：配置、修订及来源范围先替换并接通，再删除旧表。 |
| `profile_revisions` | REPLACE | R1：配置、修订及来源范围先替换并接通，再删除旧表。 |
| `profiles` | REPLACE | R1：配置、修订及来源范围先替换并接通，再删除旧表。 |
| `retrospective_requests` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `review_requests` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `root_budgets` | KEEP | 根预算保留，与 task_roots 关系保留。 |
| `runs` | KEEP | 五角色、父子运行、冻结快照保留；Profile 配置字段按 R1 替换。 |
| `search_requests` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `session_context_scopes` | KEEP | 材料/历史来源范围保留，不自动共享其他独立对话。 |
| `session_plan_revisions` | KEEP | 会话待办修订保留。 |
| `session_plans` | KEEP | 会话待办保留，不是跨对话记忆。 |
| `session_summaries` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `sessions` | KEEP | 会话和委派子会话保留；profile_id 按 R1 替换。 |
| `skill_drafts` | KEEP | Skill 候选、采用及撤销保留。 |
| `steps` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_artifacts` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_attempts` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_continuations` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_coordination` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_events` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_inputs` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_items` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_plan_revisions` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_retrospectives` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_reviews` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `task_roots` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `tool_calls` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |
| `worker_observations` | KEEP | 保留现有存储职责、关系、查询和行为测试；按执行契约适配。 |

## IPC

覆盖 `internal/ipc` 非测试 Go 文件中的字符串 case 路由，重复分派合并。删除路由不授权删除它调用的共享函数。新 RuntimeConfig 读写路由必须在实现时加入本表并记录名称。

| 路由 | 操作 | 边界 |
|---|---|---|
| `approvals.pending` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `calls.approve` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `calls.deny` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `calls.list` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `calls.resolve` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `components.list` | KEEP | 保留程序五角色查询；不是 Profile 库接口。 |
| `configs.get` | 新增（R1，2026-10-17） | RuntimeConfig 等价读取；替代 `profiles.get`。 |
| `configs.list` | 新增（R1，2026-10-17） | RuntimeConfig 等价读取；替代 `profiles.list`。 |
| `configs.restoreDefault` | 新增（R1，2026-10-17） | 恢复内置默认组合，按预期修订比较；替代 `profiles.restore`。 |
| `configs.update` | 新增（R1，2026-10-17） | RuntimeConfig 等价更新，含资源校验与预期修订比较；替代 `profiles.update`。CLI 命令：`config list/show/update/restore-default`。 |
| `contexts.get` | KEEP | 保留授权读写；仅移除记忆字段。 |
| `contexts.update` | KEEP | 保留授权读写；仅移除记忆字段。 |
| `conversations.create` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.get` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.list` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.options` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.searchAvailability` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.searchRecords` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `conversations.select` | 已删除（2026-10-17） | D2：旧 Profile 产品接口；桌面端改用 Wails `SelectConversationConfig`，CLI 会话选择已移除。 |
| `conversations.start` | KEEP | 保留对话能力；配置来源改为 RuntimeConfig，不隐式读取其他对话。 |
| `credentials.status` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `drafts.apply` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `drafts.discard` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `drafts.edit` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `drafts.list` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `drafts.revert` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `drafts.start` | 已删除（2026-10-17 前） | D2：Profile 草案接口随草案产品面删除；Skill 草案走 `skillDrafts.*`。 |
| `exfil.grant` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `exfil.list` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `exfil.revoke` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `memory.add` | 已删除（2026-10-17） | D3：独立长期记忆专属接口；CLI memory 组、Wails 门面方法与 MemoryPanel 一并移除。 |
| `memory.delete` | 已删除（2026-10-17） | D3：独立长期记忆专属接口；CLI memory 组、Wails 门面方法与 MemoryPanel 一并移除。 |
| `memory.export` | 已删除（2026-10-17） | D3：独立长期记忆专属接口；CLI memory 组、Wails 门面方法与 MemoryPanel 一并移除。 |
| `memory.list` | 已删除（2026-10-17） | D3：独立长期记忆专属接口；CLI memory 组、Wails 门面方法与 MemoryPanel 一并移除。 |
| `memory.update` | 已删除（2026-10-17） | D3：独立长期记忆专属接口；CLI memory 组、Wails 门面方法与 MemoryPanel 一并移除。 |
| `plans.clear` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `plans.get` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `profiles.archive` | 已删除（2026-10-17） | D2：旧 Profile 产品接口；存储/应用层方法一并移除。 |
| `profiles.create` | 已删除（2026-10-17） | D2：旧 Profile 产品接口；R1 后不再提供新建配置入口，内置默认配置自动创建。 |
| `profiles.delete` | 已删除（2026-10-17） | D2：旧 Profile 产品接口；存储/应用层方法一并移除。 |
| `profiles.export` | 已删除（2026-10-17） | D2：分享产品面删除；应用层 ExportProfile、Wails 门面与前端导出入口一并移除。 |
| `profiles.exportWorkers` | 已删除（2026-10-17） | D2：旧 Profile 产品接口。 |
| `profiles.get` | 已删除（2026-10-17） | D2：由 `configs.get` 替代。 |
| `profiles.import` | 已删除（2026-10-17） | D2：分享产品面删除；应用层 ImportProfile、Wails 门面与前端导入入口一并移除。 |
| `profiles.importPreview` | 已删除（2026-10-17） | D2：分享产品面删除；应用层 PreviewProfileImport、Wails 门面一并移除。 |
| `profiles.list` | 已删除（2026-10-17） | D2：由 `configs.list` 替代。 |
| `profiles.references` | 已删除（2026-10-17） | D2：配置引用随跨配置 worker 引用删除（profile_references.go）。 |
| `profiles.replaceArchive` | 已删除（2026-10-17） | D2：旧 Profile 产品接口。 |
| `profiles.restore` | 已删除（2026-10-17） | D2：由 `configs.restoreDefault` 替代。 |
| `projects.bind` | 新增（P1，2026-10-17） | 对话绑定/解绑音乐工程；Agent 工具只能触及绑定工程。 |
| `projects.create` | 新增（P1，2026-10-17） | 新建空工程（PPQ 默认 480）；与 CLI/GUI/Agent 共用。 |
| `projects.delete` | 新增（P1，2026-10-17） | 删除工程并清除对话绑定；对话与消息保留。 |
| `projects.edit` | 新增（P1，2026-10-17） | 提交整组编辑事务：opID 幂等、baseRevision 冲突拒绝、原子应用；CLI `projects edit`。 |
| `projects.export` | 新增（P1，2026-10-17） | 导出 SMF（严格校验，临时文件+改名原子落盘）；CLI `projects export`。 |
| `projects.get` | 新增（P1，2026-10-17） | 读取完整工程状态；CLI `projects show`。 |
| `projects.history` | 新增（P1，2026-10-17） | 线性编辑历史（edit/undo/redo 行）；CLI `projects history`。 |
| `projects.import` | 新增（P1，2026-10-17） | 严格导入 SMF 0/1（PPQ）；不支持格式明确拒绝；CLI `projects import`。 |
| `projects.list` | 新增（P1，2026-10-17） | 工程列表（不含状态体）；CLI `projects list`。 |
| `projects.query` | 新增（P1，2026-10-17） | 范围读取：trackId/clipId 与半开 tick 窗口过滤；`project__query` 工具与 CLI `projects show --track/--clip/--start/--end` 共用。 |
| `projects.redo` | 新增（P1，2026-10-17） | 重做最近被撤销的编辑（opID 幂等）；CLI `projects redo`。 |
| `projects.transaction` | 新增（P1，2026-10-17） | 按 opID 查询已提交事务的真实结果（中断后确认）；CLI `projects transaction`。 |
| `projects.undo` | 新增（P1，2026-10-17） | 撤销最近未撤销编辑（opID 幂等）；CLI `projects undo`。 |
| `profiles.update` | 已删除（2026-10-17） | D2：由 `configs.update` 替代。 |
| `runs.children` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `runs.get` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `runs.list` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `sessions.messages` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `skillDrafts.apply` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `skillDrafts.discard` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `skillDrafts.edit` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `skillDrafts.list` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `skillDrafts.start` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `skillDrafts.withdraw` | KEEP | 保留 Skill 改进全链路、improver 与共享证据。 |
| `status` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `stream.pending` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `summaries.get` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.cancel` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.continue` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.graph` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.retrospect` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.retrospectives` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.review` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `tasks.reviews` | KEEP | 保留当前行为及调用入口；技术详情可移动，服务不可删。 |
| `templates.create` | 已删除（2026-10-17 前） | D1：四套通用模板专属接口；存储目录、应用方法、Wails 门面与 CLI 组一并删除。 |
| `templates.list` | 已删除（2026-10-17 前） | D1：四套通用模板专属接口；存储目录、应用方法、Wails 门面与 CLI 组一并删除。 |
| `templates.restore` | 已删除（2026-10-17 前） | D1：四套通用模板专属接口；存储目录、应用方法、Wails 门面与 CLI 组一并删除。 |

核对基线：37 张表，69 个唯一 IPC 路由。CLI、Wails 门面及共享模块边界见执行契约 §4–§6；本表不声称枚举所有 Go 函数或前端组件。实施更新（2026-10-17）：`templates.*`、`drafts.*`、`profiles.*`、`conversations.select` 已按上表删除；新增 `configs.list/get/update/restoreDefault` 与 CLI `config list|show|update --file <路径>|restore-default [--revision N]`。D3：`memory.*` 路由、`context_memories` 表（schema v28）、ContextPolicy.Memory、RuntimeConfigData.MemoryEnabled、local__memory_save 与记忆注入已删除；`summaries.get`、`plans.get/clear` 与会话摘要/计划服务保留并移至独立文件。P1（2026-10-17）：schema v29 新增 `projects`、`project_revisions` 两表，`sessions` 增加 `project_id` 外键；新增上表 13 个 `projects.*` IPC 路由（当前 server.go 分发表共 57 个唯一路由字面量）与 Agent 工具 `project__query`/`project__edit`、CLI `projects` 组、Wails `ProjectService` 门面。
