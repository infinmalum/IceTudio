> 历史归档：不得据此决定当前删除范围。以 [P0 执行契约](../p0-refactor-contract.md)及[逐项清单](../p0-api-storage-inventory.md)为准；内部多角色执行、调度、审查、复盘和恢复保留。

> 历史档案：2026-10-16 转型前的 ICEPick 文档。阶段状态、产品要求和版本号仅作历史证据，不作为 IceTudio 当前待办。当前以[新实施计划](../../IMPLEMENTATION_PLAN.md)和[新架构](../architecture.md)为准。

# ICEPick

ICEPick 是一个本地优先的 AI 助手应用。连接本机 Ollama / LM Studio，或填写 OpenAI、Anthropic、Gemini 的模型与密钥后，即可聊天、上传资料、读取网页和处理获准的文件。桌面 App 面向普通用户，默认不要求理解 Agent 配置。

从[桌面使用指南](../desktop.md)开始。聊天、助手管理、设置和任务详情各有独立入口；需要高度自定义时，可以编辑助手的工具、Skills、组件及工作流程。同一二进制也提供面向开发者的 CLI。

当前已实现直接执行、动态协作、审查与有限返工、配置改进草案，以及固定流程的依赖、输入等待和显式续跑。条件分支、固定流程返工与限定自动采用仍待完成。模板已通过模拟场景验证，真实模型与安装验收独立跟踪。架构与开发范围见[设计文档](icepick-architecture-2026-10-16.md)和[实施计划](icepick-implementation-2026-10-16.md)。

## 当前功能

文件覆盖需预期内容，精确编辑要求唯一匹配；本地命令返回退出码、输出、超时及截断标记。工作区及可执行文件由本机权限控制，命令执行仍需审批，未提供完整沙箱。MCP 工具先通过 `local__tool_search` 搜索加载，下一轮调用，保留 schema 指纹及实际调用记录。

- 直接选择 Profile 开始对话，无需创建 Agent；新对话默认隔离上下文，可显式沿用已有对话的记忆与权限。Profile 切换按对话保存，保留历史和授权，在途 Run 使用旧快照。
- main/worker 组件支持独立提示词、模型和工具子集；worker 来源可选当前或其他 Profile 的配置，运行固定修订、模型与所选 SKILL.md 内容；支持 direct、dynamic 与 fixed 静态阶段流程。出厂模板可创建、编辑和按配置组恢复，删除本地 Profile 不删除程序模板、历史或授权。
- Profile schemaVersion=1、事务修订记录和归档删除；v6 分享包含流程、planner、预算及独立 worker 配置，只读取当前格式，不携带本机授权和出厂绑定。
- 按 LocalContext 隔离会话、消息和任务记录；支持多轮对话、工具调用、流式输出与取消任务。
- 统一接入 MCP 服务和 Agent Skills，由 Profile 选择启用范围，与对话本机授权取交集执行。
- 工具执行统一经权限网关：effect/sensitivity 分类、全人工/半自动/全自动三模式审批（绑定调用与参数哈希）、工作区硬边界、MCP 指纹变化暂停自动执行、每轮模型发送前的数据外发授权检查（桌面端可管理外发授权与远端凭据）。
- 上下文压缩：会话超过 Profile 上下文预算时自动把最旧的完整轮次归纳为摘要（保留当前任务与未闭合工具调用，原文不删），摘要继承数据范围并继续受外发检查约束；会话视图可查看摘要与覆盖区间。
- 按本机上下文隔离的长期记忆：记忆按 `context_id` 隔离，Profile 偏好与本机上下文授权同时开启才生效；运行开始时注入（来源与范围标注，范围计入外发检查），模型可经 `local__memory_save`（需审批）写入；桌面记忆面板与 `icepick memory` 命令提供查看/编辑/添加/删除/导出。
- 任务计划：模型经 `local__update_plan` 维护带稳定 ID、依赖、验收条件与材料出处的计划；宿主校验依赖和修订，防止并发覆盖及已开始任务被重写。桌面/CLI 显示计划，重启后恢复；清除保留修订历史。此处会话待办状态由模型记录；根任务的 dynamic 自动调度与独立审查使用另一套实际执行状态。
- [固定阶段流程](../fixed-workflows.md)：Profile 定义静态阶段和依赖，宿主无需 planner 即可调度；支持只读并行汇合、写入串行、main 汇总与审查门槛。桌面可编辑阶段，分享和配置组恢复保留结构；首版失败停止，不支持条件分支、用户输入等待、自动返工或中断续跑。
- 动态协作：Profile 选择 dynamic 后，planner 按关键事件生成/修订依赖计划，宿主审批、领取并调度，main 整理交付。只读任务并行，写入和未知副作用串行；waiting/blocked/failed/complete 状态明确。根预算覆盖所有组件与重试，模型用量未知时保留预留并标记；安全自动重试需显式开启。重启保留游标与尝试，进入阻塞，不盲目重放。
- 本次任务执行计划：启用规划、委派和 worker 槽位后，main 可声明依赖任务，经审批按 ID 与条件指纹执行。宿主原子领取并关联子 Run、执行尝试和前置交付文本；状态由实际执行推进，未执行任务不能标记完成，中断和未知结果不会自动重跑。桌面与 `tasks status` 可查看完整计划和尝试；dynamic 已支持 planner 与受控只读并行；可配置独立 reviewer 审查。
- 本机委派：Profile 配置 worker 槽位后，main 经 `local__delegate_task` 按槽位派发子任务；来源可为当前配置或其他 Profile 的 worker 投影。所有子任务使用当前对话的本机上下文与授权，冻结配置和资源上限，取消父任务会传播到子任务。
- Profile 分享包 v6：可明确选择包含 worker 来源，展平为独立配置；一键导出 zip（配置清单、人格/提示词独立文件、MCP 依赖引用、标准 Skills 目录），不含授权、会话、记忆、密钥与绝对路径；导入先预览缺失依赖与建议，确认后创建新 Profile（不自动安装资源、不创建本机上下文、不授权）。桌面端与 `icepick profiles export/import` 均可使用。
- CLI/IPC：桌面端或 `icepick serve` 构成每数据目录唯一宿主；`icepick` 命令行经本机 IPC 驱动与桌面相同的服务契约。

当前内置 read_file/write_file/edit_file/exec、tool_search，以及 Skill/记忆/计划/委派和过程检索工具；外部 MCP 工具已可经权限网关和审批调用，统一按 external/unknown 处理。已提供通用文件写入/精确编辑和本机命令执行；OpenAI、Anthropic 和 Gemini 原生联网搜索已接入；Jev、A2A 尚未实现。新对话默认半自动审批、一般资源需显式授权，远端外发另行授权；长期记忆与委派需在 Profile 偏好与本机上下文授权（记忆授权 / 委派允许列表）中同时开启。新 worker 使用对话本机授权，配置来源不提供权限或记忆。会话 Plan 保留待办语义；direct 支持 main 同步委派，dynamic 使用 planner 和宿主调度。动态交付物带消息引用、SHA256 与真实工具证据；worker 可按本根任务交换过程与观察，继承数据外发范围。Profile 可选择 off/on_demand/required 独立审查；required 未通过不能完成，结论绑定目标、交付文本及工具证据指纹。必要检查需指定工具成功执行并返回 JSON `{"passed":true}`，无法确认时为 inconclusive。桌面可单次复核结果；CLI `tasks review <sessionID> <runID>` 发起复核、`tasks status` 查看结论；旧任务终态保留。reviewer 只读取证据；required 模式将缺陷交回 main，dynamic 模式由 planner 给原 worker 新建修正任务。最多返工两轮，共享原根预算、权限和取消信号；缺失检查由执行组件通过现有工具关口补做，返工后需要新检查证据。拒绝必要操作、效果未知或预算耗尽时停止；旧交付与审查记录保留。按需单次复核不自动返工；桌面“复盘此任务”和 CLI `tasks retrospect <sessionID> <runID>` 结合已结束任务的真实过程输出观察、建议与限制，支持无最终交付的失败任务；结论单独保存，不改变旧终态或 Profile/Skill，也不代表审查通过；worker 的 succeeded/unreviewed 仍不等同于审查通过。

开发阶段不承诺旧数据或旧接口兼容。当前数据库格式为 v20、分享包格式为 v6；直接创建当前结构，遇到其他版本明确拒绝，不迁移、不自动删除数据。需要试用新版时使用新的数据目录。此政策持续到项目开始有实际用户并由维护者明确通知。MCP 服务在任务首次发现所选服务工具时启动。

## 运行

需要 Go 1.25+、Node.js、npm、Wails 3 CLI，以及当前平台的 Wails 桌面依赖。在本目录执行：

```sh
wails3 dev
```

构建 Linux 桌面应用：

```sh
wails3 build
```

产物在 `bin/icepick`。目前只验证了 Linux 构建。依赖使用 Go/npm 的用户级持久缓存；若需自定义缓存，应放在本项目 `.cache/`，不要放在系统临时目录。

无需模型或密钥也可启动；界面会显示“模型未连接”。要运行任务，先在“模型服务”选择提供方并保存模型 ID：本机 Ollama/LM Studio 确保服务已连接；远端 OpenAI/Anthropic/Gemini 需在“远端凭据”面板保存 API 密钥（保存在数据目录内的 0600 文件，不会回显），并按需在当前对话的“数据外发授权”面板允许该端点。本机模型调用支持流式输出，界面会实时显示生成文本。

全部应用数据默认保存在二进制文件旁的 `data/`，与启动时的工作目录无关。配置文件为 `data/config.json`；未创建时默认使用 Ollama 的 `http://127.0.0.1:11434/v1`。本机模型地址必须指向本机 HTTP 服务。数据库 `icepick.db`、日志 `icepick.log`、凭据 `credentials.json` 与 IPC 套接字以及 `skills/` 均位于同一个 `data/` 下。桌面 App 和 CLI 共用此规则。AppImage 使用 `.AppImage` 文件旁的 `data/`；普通二进制通过符号链接启动时使用真实二进制旁的目录。应用所在目录必须可写，失败会明确报错，不回退到用户目录。

## 命令行

每个数据目录同时只有一个宿主：桌面应用或无头宿主。CLI 命令连接正在运行的宿主：

```sh
bin/icepick serve            # 启动无头宿主（Ctrl+C 退出）
bin/icepick status           # 宿主与模型状态
bin/icepick profiles list    # Profile 列表（create/copy/edit/restore/export/import/delete）
bin/icepick profiles draft <conversationID> "创建一个初学者解释助手"
bin/icepick skill-drafts generate <conversationID> "提炼复核方法" --evidence <runID>
bin/icepick skill-drafts list
bin/icepick profiles drafts list
bin/icepick profiles drafts show <draftID>
bin/icepick profiles drafts apply <draftID> --revision <草案修订>
bin/icepick templates list   # 可用场景模板
bin/icepick templates show development   # 输入、交付、准备条件及示例
bin/icepick templates create daily   # 创建日常助手 Profile
bin/icepick conversations create --profile <profileID>   # 新对话，自动隔离
bin/icepick conversations start <conversationID> "任务内容" --wait
bin/icepick conversations permissions <conversationID> --mode semi --workspace <项目路径>
bin/icepick conversations select <conversationID> --profile <profileID>
bin/icepick conversations show <conversationID>   # 查看对话历史
bin/icepick tasks status <runID>   # 任务状态与委派子任务树
bin/icepick approvals list   # 待审批与结果未知调用（含委派子会话）
bin/icepick memory list <contextID>   # 长期记忆（add/edit/delete/export）
bin/icepick logs 100         # 查看宿主日志
bin/icepick help             # 完整命令列表
```

配置草案的使用说明和限制见[Profile/Skill 草案指南](../profile-drafts.md)。任务证据必须明确选择，Skill 采用发布独立副本；分享派生配置前需人工检查内容，CLI 使用 `--reviewed` 保存经核对的分享包。

四个模板的使用说明、案例与验收范围见[默认场景指南](../scenarios.md)。开发任务可使用内置读写编辑/exec，无需另装 MCP；CLI 用 `conversations permissions <conversationID> --command <可执行文件>` 设置命令权限，该选项替换当前命令列表。

无交互执行遇到审批请求时只报告、绝不默认放行；可在桌面端或用 `approvals approve/deny` 处理。

构建前运行核心测试：

```sh
go test ./internal/...
```

执行 `wails3 build` 后运行包含桌面服务层的测试：

```sh
go test ./...
```

## 目录

- `main.go`：入口；无参数启动 Wails 桌面，带参数进入 CLI。
- `statusservice.go`：模型配置、Profile/LocalContext、会话、任务与凭据 Wails 服务。
- `internal/config/`：本地配置、提供方校验与凭据存储。
- `internal/model/`：统一模型客户端（本地兼容 + OpenAI/Anthropic/Gemini 官方 SDK 适配、流式）与模型发现。
- `internal/agent/`：共享的 Agent 多轮循环、流式回调、上下文压缩、取消和轮次上限。
- `internal/app/`：界面无关的应用服务（运行编排、权限网关、外发检查、流缓冲、记忆与摘要服务）。
- `internal/host/`：桌面与无头宿主共用的启动链路。
- `internal/ipc/`：本机 IPC 服务器/客户端（JSON 行协议、令牌 + 同 UID 校验）。
- `internal/cli/`：命令行客户端。
- `internal/mcptools/`：使用官方 Go MCP SDK 连接工具服务；内置时间工具由本地 MCP 服务提供。
- `internal/skills/`：发现并按需读取 Agent Skills。
- `internal/storage/`：SQLite Profile/LocalContext、隔离会话、消息与任务状态。
- `frontend/src/App.tsx`：界面组装；`components/` 保存各功能面板，`hooks/` 分别管理工作区、对话执行、Profile、本机上下文和模型设置，`shared/` 保存常量与下载工具。
- `build/`：Wails 平台构建配置。

## 文档

- [设计文档](icepick-architecture-2026-10-16.md)：以 Profile 为入口的新目标架构与可自定义能力。
- [实施计划](icepick-implementation-2026-10-16.md)：新阶段与验收标准。
- [历史实施记录](implementation-v1.md)：现有 P0–P8 基线及未完成的真实验收。
- [MCP 与 Skills](../mcp-and-skills.md)：当前接入方式。

## 许可证

本项目采用 GNU Affero General Public License v3.0（`AGPL-3.0-only`），完整条款见 [LICENSE](../../LICENSE)。第三方依赖与保留版权声明的构建脚本遵循各自许可证。


## 对话联网搜索

在对话输入框旁开启“联网搜索”，模型可按需搜索；开关属于对话，发送时冻结到任务，不改变在途任务。CLI 使用 `icepick conversations search <对话ID> on`、`off` 或 `records` 查看记录。

原生搜索接入 [OpenAI Responses](https://developers.openai.com/api/docs/guides/tools-web-search)、[Anthropic Messages](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool) 和 [Gemini Interactions](https://ai.google.dev/gemini-api/docs/google-search)，支持流式返回、引用和普通函数工具共存。Anthropic 使用 web_search_20260318 且明确 allowed_callers=direct，不启用云端代码过滤。Gemini 3/4 文本模型使用新版官方 SDK 的 Interactions API，以 store=false 及完整协议步骤在宿主保存会话；搜索关闭后继续使用该协议保持签名和函数结果。未知模型、Gemini 2.x 和专用图像/音频模型不猜测混合搜索能力，可选择已配置 MCP。账户、模型和自定义端点的实际支持在请求时验证；请求错误不会静默换服务或重试。搜索查询及网页结果需要 unknown 外发范围，其他会话材料继续遵循实际范围；开启开关不新增外发授权。即便服务商通过本机网关接入，原生搜索仍需向该端点授权查询材料范围。组件工具白名单限制原生搜索时使用 `local__web_search` 这一能力名称，它不会作为普通函数工具暴露给模型。planner、reviewer 和摘要器继续使用已有证据，只读 worker 不获得外部搜索能力。

其他模型接口可使用已配置的搜索 MCP：在对应 `mcpServers` 项添加 `"webSearchTools": ["真实搜索工具名"]`，并在 Profile 选择该服务/工具。开启时提供登记的真实 schema，调用仍需权限和审批；关闭时不提供这些专用搜索工具。没有可用路径时显示原因，不自动安装搜索服务。普通命令和其他 MCP 的联网行为仍由原权限机制控制。

搜索记录保存实际事件、来源链接及能取得的用量；网页材料属于未知来源。OpenAI 和 Anthropic 每请求最多预留 3 次搜索/网页读取，共享根工具预算，完整响应释放未用预留。Gemini 没有服务商内部搜索次数的硬上限参数，按每个宿主搜索请求预留并消耗 1 个预算单位，内部次数与独立费用未报告时明确显示未知；不会把查询条数当作实际调用/账单次数。中断保留未知结果与预留，不自动重试或重放。Google 搜索建议保存并在隔离、禁脚本的 iframe 中呈现；不把服务商 HTML 放进宿主页。真实服务及稳定场景验收仍待完成。

当前数据库格式为 **v26**。Profile 定义工作方式，LocalContext 只拥有本机权限和私人状态，Conversation 明确选择 Profile，Run 固定执行配置。新建对话才创建 Context，宿主没有隐式默认 Context。消息、运行、计划和摘要的 Context 由 Session 推导，不重复保存归属列。任务查询、取消和工具审批只需资源 ID；CLI 使用 `tasks status <runID>`、`tasks cancel <runID>`、`approvals approve <callID>`，待审批列表可用 `approvals list --context <contextID>` 筛选。旧数据库格式明确拒绝，不迁移、不删除原文件。

### 对话附件与网页材料

输入框旁的“添加附件”支持图片、PDF、文本和 DOCX；Profile ZIP 包使用独立的“导入 Profile / 导出 Profile”。开启已有联网搜索后直接粘贴 URL，可按需读取指定页面；本地目录直接写入消息，由现有 read_file 列目录并读取授权文件。Skill 的 references/scripts/assets 随任务冻结并由 load_skill 按需读取，脚本执行复用本地 write_file/exec。格式、大小、权限及各服务商行为见[材料输入说明](../materials.md)。
