# P1 工程模型与统一编辑事务——阶段设计

更新：2026-10-18。本文是[实施计划 P1](../IMPLEMENTATION_PLAN.md) 实现前必须明确的规格（计划 §5），覆盖正常、边界、冲突与失败案例。实现与本文冲突时，先改本文并说明理由，再改代码。

## 1. 工程格式与保存原子性

- 工程与对话同库存放，但独立于会话存活：`projects` 行保存标题、PPQ、修订号与完整状态 JSON；`sessions.project_id` 是对话到工程的显式绑定，可为空。删除对话不动工程；删除工程时在同事务清除绑定，对话历史保留。
- 每次成功事务（含撤销/重做）在**同一个 SQLite 写事务**内更新 `projects` 行（状态 + 与最终 `State.Title` 相同的 title + 修订号 + updated_at）并追加一行 `project_revisions`。列表和详情元数据必须与同修订的状态名称一致；不能仅在前端改名或读取时掩盖存储分歧。SQLite 保证工程行与历史同时生效或同时不生效；进程崩溃后启动读到的是上次一致状态，无需专门恢复步骤。
- 修订号从 0（空工程）开始，每次成功事务 +1，只增不减。撤销/重做也产生新修订（见 §5），不回写历史。
- 状态 JSON 上限 64 MiB；超限拒绝提交，原状态不变。
- 开发格式版本号随之升到 29；旧版本数据库按现行政策明确拒绝并保留文件，不做迁移。

## 2. tick / PPQ 精度与转换舍入

- 工程内所有时间位置与时长为**非负 int64 tick**，分辨率为工程 PPQ（每四分音符 tick 数），建工程时固定，范围 1–32768，默认 480。工程内没有隐式量化；网格吸附属于后续编辑界面功能。
- MIDI 导入采用源文件 PPQ 建工程，不做换算；因此 P1 的往返不损失精度。
- 跨分辨率换算（未来合并/量化使用，本阶段提供并测试该规则）：`dst = (src * dstPPQ + srcPPQ/2) / srcPPQ`（正整数域，.5 一律进位）。函数 `project.ConvertTick(src, srcPPQ, dstPPQ)`；srcPPQ 或 dstPPQ 越界时返回错误而不是静默换算。
- 播放时间换算（速度图）属于播放阶段，不在本契约内。

## 3. 同音高配对、事件顺序与支持/拒绝清单

### 3.1 导入配对规则（按轨、按通道、按音高独立处理）

事件按 (绝对 tick, 轨内原始顺序) 处理：

1. Note-on（0x9n，velocity>0）：为 (channel,pitch) 开启一个待配对音符。若同 (channel,pitch) 已有未关闭音符，则旧音符在新 onset 处关闭（时长 = 新onset − 旧onset，为 0 时记 1 tick），新音符开启。
2. Note-off（0x8n，或 0x9n velocity=0）：关闭**最近**开启的同 (channel,pitch) 音符（LIFO）。时长 = off − onset，为 0 时记 1 tick。音符力度取 note-on 值。
3. 无可配对的 note-off：保留为原始事件（RawEvent），不丢弃、不报错。
4. 轨结束时仍未配对的 note-on：在轨结束 tick（EOT 位置）关闭；若等于 onset 则时长记 1 tick。
5. 音符归属：源轨 → 工程 Track，其音符放入该轨唯一 Clip（起点 0，长度 = 轨结束 tick，最小 1），局部 tick = 绝对 tick。

### 3.2 同 tick 事件顺序

- 工程内：事件保存导入/编辑顺序，不重排。
- 导出：同一 (tick, channel, pitch) 上 note-off 先于 note-on（保证同音高重复触发往返一致）；其余事件按存储顺序稳定排序。

### 3.3 支持 / 保留 / 拒绝清单

- **支持并结构化**：note on/off；CC 0–127；pitch bend；program change；channel aftertouch；poly aftertouch；meta 0x51 速度（→ TempoMap，微秒/四分整数保存）、0x58 拍号（→ MeterMap，分母限 2^k，k∈0..7）、0x2f EOT。
- **保留原始字节**（RawEvent，单条 ≤ 16 KiB）：SysEx（F0/F7）、无配对 note-off、未知 meta、0x54 SMPTE offset、0x59 调号、0x20/0x21 port、文本类 meta（0x01–0x07）。
- **明确拒绝**（报错、不产生半成品工程）：SMPTE 时间格式（division 最高位为 1）、SMF format 2、MThd 块长度 ≠ 6、MTrk 声明长度与实际数据不符、实际轨数 ≠ 头声明轨数、文件截断（含 gomidi 会接受的 13 字节截断头——由前置结构校验拒绝）、尾部存在不足 8 字节或无法解析的残余字节。
- 绝对 tick 超过 2^31−1 的轨拒绝（防止后续精度问题）。

### 3.4 输入资源上限

| 项 | 上限 | 超限行为 |
|---|---|---|
| 文件大小 | 10 MiB | 拒绝 |
| 轨数 | 64 | 拒绝 |
| 单轨事件数 | 100,000 | 拒绝 |
| 全文件事件数 | 1,000,000 | 拒绝 |
| 单工程音符数 | 200,000 | 事务拒绝 |
| RawEvent 字节总量/工程 | 8 MiB | 事务拒绝 |

实现补充的三条确定规则：

- **format 1 指挥轨**：轨 0 若不含通道消息，其速度/拍号进映射、名称作工程名；其余 meta（调号、标记、sequencer 专属等）保留为一条“元数据”轨的 RawEvent，不静默丢弃。
- **SysEx 导出**：保存的原始字节是解码后的 `F0 … F7` 载荷，写出时按 SMF 标准重编 `F0 <varlen 长度> … F7`；通道消息与 meta 原样写出。
- **映射缺 tick 0**：导入文件的速度/拍号表若没有 tick 0 起始点，补默认 120 BPM / 4/4，而不是拒绝。

## 4. 工程模型与操作集

- 稳定对象 ID：创建时生成，不得在同一工程中复用给另一新对象；撤销/重做恢复同一对象的原 ID，不重新分配身份。客户端可在添加操作中自带 ID（同批内引用新对象），服务校验字符集 `[a-z0-9_]`（≤64 字符）与唯一性；缺省 ID 在结果中回显，并写入该次实际应用的历史操作。对象的服务生成格式为 `<前缀>_<12位hex随机>`：`t_` 轨、`c_` 片段、`n_` 音符、`e_` 事件；工程 ID 沿用存储层的随机 hex ID（原文将其也写为 `p_` 格式不符合实现，现纠正描述，不改变接口）。此前仅检查存活对象的缺口已由 §10 的历史占用检查接续；工程之间的相同显式对象 ID 不共享身份。
- 结构：Project{Title, PPQ, Tempo[], Meter[], Tracks[]}；Track{ID, Name, Channel, Program, Muted, Solo, Clips[]}（片段按起点排序，允许重叠，导出按绝对 tick 稳定排序）；Clip{ID, Name, Start, Length≥1, Notes[], Controls[], Raw[]}（事件局部 tick ∈ [0, Length)）；Note{ID, Start, Duration≥1, Pitch 0–127, Velocity 1–127}；ControlEvent{ID, Tick, Kind, Channel, Controller/Value/Amount/Program 按类取值}；RawEvent{ID, Tick, Data}。
- TempoMap/MeterMap 为工程级点列表，按 tick 升序，起点必须含 tick 0（导入时补默认值 120 BPM / 4/4）；同 tick 重复点取最后者。事务操作 `tempo.set` / `meter.set` 整表替换（编辑界面逐点交互属 P2）。
- 操作集（`kind`）：`project.rename`、`track.add|update|remove`、`clip.add|update|remove`、`note.add|update|remove`（add/update 支持批量数组）、`control.add|remove`、`tempo.set`、`meter.set`。
- 校验（整组、先于任何写入）：ID 存在性/唯一性、音高 0–127、力度 1–127、tick ≥ 0、时长 ≥ 1、事件局部 tick < Clip.Length、通道 0–15、程序 0–127、CC 值 0–127、remove 的 ID 必须存在、update 字段全为空视为错误。任一失败 → 整组拒绝，状态与修订不变（不留半组编辑）。

## 5. 事务：去重、冲突、撤销/重做与恢复

### 5.1 提交

`SubmitProjectOps(projectID, opID, baseRevision, source, ops)`：

1. 开启 SQLite 事务；若 `op_id` 已存在于该工程历史 → **返回原结果，不做任何修改**（幂等，覆盖“工程提交成功但工具记录失败”的确认场景）。当前 Store 使用默认 `BeginTx` 的 deferred 事务，未配置 `BEGIN IMMEDIATE`；原文的锁模式描述据此纠正，本批不改变锁策略。多个连接竞争可收到锁错误或修订冲突，不承诺自动排队或重试；失败不能留下半组写入。
2. `baseRevision ≠ 当前修订` → 拒绝（“工程已被其他操作修改”）。保守工程级冲突，不做对象级合并或 rebase；对象粒度按证据再细化。有新增对象的普通编辑随后在同一事务内查询本工程历史占用，按 §10 拒绝 ID 复用；所有普通编辑均拒绝非空内部恢复载荷。
3. 整组校验（§4）；在内存态按序应用，逐操作生成逆操作。返回并持久化的逆操作集按原操作的**逆序**排列，保证先撤销后做的修改，再撤销其依赖；`note.update` 内同 ID 的多个 delta 仍按输入顺序应用，其逆 delta 也按逆序排列。单个原操作的字段集合不作无关重排。
4. 写新状态（revision+1）+ 历史行（ops、inverse、result），`COMMIT`。历史 ops 的四类新增操作须包含本次实际分配的 ID（§9），而不是仍缺 ID 的原请求；不修改调用方输入。
5. 结果：`{opID, revision, applied, summary[kind]计数, createdIds}`。
- `source` 记录来源（`gui` / `cli` / `agent:<runID>`），仅入历史供追溯，不影响校验路径。

### 5.2 撤销 / 重做

- 撤销 = 追加一个 `kind=undo` 的事务，内容为**最近一个未撤销 edit 的逆操作**；该 edit 标记 `undone=1`。连续撤销依次回退更早的 edit；撤销之后的新 edit 正常入栈（线性撤销，无分支）。
- 重做 = 扫描「最后一次 edit 之后」的 undo 行，取**最近一个未被 redo 消费**的 undo，重新应用其目标 edit 的操作集并追加 `kind=redo` 行；目标 edit 的 `undone` 清零。撤销后插入新 edit，其上的 redo 全部失效（与主流 DAW 一致）。实现为 `internal/project.PlanUndo` / `PlanRedo`，存储层只执行计划。
- 撤销/重做同样携带客户端 opID 与 baseRevision，走同一幂等与冲突检查。
- 撤销执行目标事务的逆操作，不回滚整个工程快照：`track.remove` 的逆操作恢复该轨对象及其片段/事件；若用户随后又修改了别处，仍按上述线性历史依次撤销，不能跳过较新事务任意撤销某个对象。

### 5.3 恢复与真实结果查询

- 崩溃恢复依赖 §1 原子性；启动时无补写逻辑。
- 任何提交方（GUI/CLI/Agent）在中断后用 `GetProjectTransaction(projectID, opID)` 查询原结果；查到即证明已生效。不盲目重放（外部副作用未知语义在 P3 扩展）。

## 6. 三条入口共用同一服务

- 服务层：`internal/app` 的 Project 方法（List/Get/Query/Create/Delete/Submit/Undo/Redo/GetTransaction/History/SetConversationProject/Import/Export）；存储层 `internal/storage` 只负责持久化与应用 `internal/project` 的纯函数（校验/应用/逆操作/撤销重做推导）。
- 范围查询：`QueryProject(projectID, trackID, clipID, start, end)` 返回过滤副本（`internal/project.Region`）：半开 tick 窗口 [start, end)，音符按重叠计入，控制/原始事件按 tick 计入，速度/拍号作为全局上下文原样保留；不带范围即全量读取。`project__query` 工具与 CLI `projects show --track/--clip/--start/--end`、IPC `projects.query` 共用该读路径——先查区域，再构造批量事务，即“限定音轨/片段/时间范围的批量操作”。
- GUI：Wails 绑定 `ProjectService`（main.go 注册，与 StatusService 并列）直达服务；原生文件选择（`PickOpenMIDIFile`/`PickSaveMIDIFile`）在 Go 侧调用系统对话框。前端工程条提供新建/导入/导出/重命名/撤销/重做/删除与对话绑定；钢琴卷帘等编辑界面由 [P2 第一批](p2-midi-editor.md)接入，仍使用相同事务服务；P1 本身不含试听，后续已接入范围见 [P2 播放契约](p2-playback.md)。对话列表携带 `projectId`（`SetConversationProject` 绑定/解绑）。
- CLI/IPC：`projects.*` 13 个方法与 `icetudio projects` 命令组（list/show/create/delete/edit/undo/redo/transaction/history/import/export/bind），经 IPC 到同一服务。
- Agent：注册 `project__query` 与 `project__edit` 工具，仅当运行所属会话绑定了工程时进入定义列表；`project__edit` 参数即 (baseRevision, opID, ops)，走与 GUI 完全相同的 `SubmitProjectOps`。权限模型上 `project` 是原生工具族而非 MCP 服务：`RuntimeConfigData.AllowsTool` 对 `project__*` 跳过 MCP 启用检查（显式 `AllowedTools` 白名单仍可限制），委托 worker 的能力交集规则不受影响。P1 用模拟 Agent（测试直接调用工具路径）验证同一命令得到同一状态。
- 导入/导出：SMF format 1 导出（轨 0 = 速度/拍号/工程名，之后每轨一轨）；format 0 导入合并为单轨。导入总是新建工程（采用源 PPQ）；导出写临时文件后原子改名，失败保留原目标文件。

## 7. 验收对照（P1 计划条目 → 本文/测试）

| 计划条目 | 依据 | 测试 |
|---|---|---|
| 模型与稳定 ID、整数 tick | §2/§4 | `TestConvertTick`、`TestValidID`、project 包操作单测 |
| 三入口共用查询与编辑服务（含范围查询） | §6 | `TestProjectThreeEntrypointsShareOneState`（含工具范围查询）、`TestProjectsOverIPC`（含 `projects.query` 窗口过滤）、`TestRegionFilters` |
| 修订/操作 ID/整组校验/原子提交/撤销重做/结果可查 | §5 | `TestProjectTransactionReplayAndConflict`、`TestProjectUndoRedoPersistence`、`TestBatchIsAtomic` |
| 工程独立于对话；保存/重开/崩溃恢复 | §1 | `TestProjectLifecycleAndIndependence`、`TestProjectDeleteCascadeHistory`、`TestProjectUndoRedoPersistence`（重开恢复） |
| gomidi 适配与拒绝清单 | §3.3 | `TestRejections`、`TestOversizeInputRejected` |
| 配对/PPQ/排序确定规则；保留控制与原始事件 | §3 | `TestImportSemantics`、`TestExportImportRoundTrip`、`TestUnmatchedOpenNoteClosesAtEnd` |

## 8. 2026-10-18 名称持久化与依赖批量撤销修正范围

本节是在 P2 保存/重开回归中发现的 P1 缺陷的有效修正边界；已完成下述代码及限定验证，证据见[增量记录](validation/p2-project-history-2026-10-18.json)。

- REPLACE：存储层 edit/undo/redo 的 UPDATE 改为同时写最终状态名称；领域层新生成的逆操作/逆 delta 按依赖逆序执行，替换错误的正序结果。
- KEEP：所有 GUI/服务/CLI/IPC/Agent 入口、操作参数、opID 原结果优先、修订冲突、整组原子性、既有历史与结果查询、控制/原始事件、授权与多 Agent 运行机制。无数据库格式或依赖变更，不读取旧格式，不在启动/读取时批量改写已有数据；普通事务仍按 §1 更新工程。
- ADD：真实 SQLite 的列表/详情/重开、失败回滚测试，以及领域层依赖批量往返、浏览器名称交互和 IPC 的跨入口回归。UI 继续查询服务，无前端专用名称或撤销修补。
- 验收：重命名后列表/详情/状态同名，撤销与重做及重开仍一致；两次改名整体撤销回原名；延长片段后加音符可整体撤销；同音符连续 delta 可回原值；非法/过时/历史写入失败不留下名称、状态、历史或结果的部分变更；旧 opID 返回原结果而不覆盖当前名称。
- 限制：不在启动或读取时批量修补此前由缺陷写出的元数据或逆操作；此后的成功事务会同步其最终状态名称，新生成的逆操作遵循上述顺序。既有错误逆操作不重算，查询原结果不能证明它正确；本批不增加历史重算或迁移路径。真实 Wails 交互与断电验收仍属 P2/P5 未完成项。

## 9. 服务分配 ID 的重做稳定性修正

状态：2026-10-18 实施前界定，现已完成下述代码及限定验证；证据见[验证记录](validation/p2-stable-ids-2026-10-18.json)。此增量落实 §4 的稳定身份与 §5 的共享撤销契约，不增加编辑能力。

- REPLACE：历史中的新增操作保存为实际应用的操作：`track.add`、`clip.add`、`note.add`、`control.add` 的缺省 ID 补为本次已生成的 ID。仍按原操作和数组顺序记录，显式 ID 保持原值；不得从最终状态反推，以免漏掉同批内随后删除的对象。
- KEEP：客户端参数、结果结构、所有入口、修订检查、opID 去重、整组原子性、历史格式、权限及执行内核。调用方的输入对象不被修改；失败不发布状态或半组历史。重做复用原 ID，之后的撤销和其他已记录的对象引用继续有效。
- ADD：领域层返回实际应用操作的内部函数，存储层在既有事务内保存其结果；GUI/CLI/IPC/Agent 无需各自补 ID。不增加 schema、旧历史重建或兼容路径；以前保存的缺省 ID 历史不追溯修复。
- 验收：四类缺省 ID、显式/缺省混用、多轮撤销/重做、后续编辑的历史引用、重开、原 opID 查询/重放一致；失败/过时/历史写入失败不留下状态、历史或结果的部分变化。真实服务的浏览器/IPC 路径须覆盖服务分配 ID 的往返，原生 WebView 与断电不由此推定。
- 已验证：领域层实际操作与结果 ID 一致、调用方输入不变、同批内后续删除的显式对象仍记录；SQLite 四类新增各三轮重开撤销/重做，再两轮依赖编辑栈往返；edit/undo/redo 的历史写入故障原子回滚；IPC 的五事务栈两轮往返；浏览器的四事务栈及后续音符修改往返。原有过时修订回归继续保留。
- 限制：仅修正此后生成的历史，不给旧记录补 ID；旧缺省 ID 操作仍可能在重做时换 ID。新操作重放仍返回原结果且不写状态；这不等于修复旧历史。未验证任意操作组合、真实 Wails 进程崩溃或断电。

## 10. 工程生命周期内的对象 ID 防复用

状态：2026-10-18 实施前建立，现已完成下述限定实现与验证，证据见[本批记录](validation/p2-identity-reuse-2026-10-18.json)。接续 §9，补齐 §4 中已登记的历史防复用缺口；旧记录中关于该缺口未实现的描述是其当时状态。

- REPLACE：普通新编辑的唯一性检查由仅存活对象，扩展为同一工程的存活及历史占用 ID；轨道、片段、音符、控制和原始事件共用对象命名空间。ID 一旦随成功事务生效，即使之后删除、撤销创建或重做分支失效，也不能分配给另一新对象。同批内先删除再新建、先创建再删除再新建也拒绝。失败事务不占用 ID；其他工程可使用相同显式对象 ID，工程 ID 继续隔离目标。
- ADD：领域层明确区分新编辑与服务规划的历史执行；存储层在原有 SQLite 事务内、opID 去重和修订检查后，为有新增操作的请求读取本工程历史占用。占用依据是已存结果的 `createdIds` 与逆操作中的对象 ID/目标引用，包括导入对象在父轨/片段删除时保存的嵌套音符、控制及 RawEvent。无需重算历史、增加表、schema 升级或迁移；不写缓存、墓碑文件或第二份音乐状态。仅更新/删除的请求不扫描历史占用。
- KEEP：共享 GUI/CLI/IPC/Agent 入口、参数及结果；原 opID 返回原结果，过时修订拒绝，整组原子性，历史及项目独立保存。撤销/重做只执行服务从已存历史规划的操作，恢复同一对象时允许原 ID；不能按客户端 `source` 或其他字符串放宽新编辑检查。
- 普通新编辑禁止非空 `invClips` / `invNotes` / `invControls` / `invRaw` 载荷：它们仅供服务保存的逆操作使用，不能通过普通新增注入嵌套对象、绕过防复用/普通参数校验。保留原有内部逆操作及嵌套恢复；不把这些内部字段当作客户端恢复接口。
- 执行顺序：先建立本契约和失败回归；接入新编辑与历史执行的分流、只读历史检查；再验证四类新增、导入嵌套/原始对象、跨类型复用、同批复用、撤销后占用、分支失效、失败不占用、重开、跨工程隔离、并发及原结果查询。服务/IPC/浏览器须证明拒绝不会改动状态、修订、历史或结果，随后仍可正常撤销恢复。
- 已验证：领域层新编辑/历史执行边界；SQLite 四类新增在删除、重开后拒绝复用并可撤销/重做；导入形态的父轨/片段删除后，嵌套音符/控制/RawEvent ID 继续占用并完整恢复；同批复用、四类非空恢复载荷、跨类型、撤销与分支失效、失败不占用、跨工程隔离、两连接并发及另一个 Store 读取历史；IPC 拒绝四类 ID 和伪装来源；浏览器实际选择/删除、共享服务拒绝及正常撤销/重做。累计 102 项浏览器检查通过（工程服务/SQLite 真实，Wails、音频及退出门面模拟）。
- 限制：保留当前完整编辑历史是检查前提；尚无历史裁剪功能，未来裁剪必须先另行设计占用保留。历史 ID 查询随已存历史量增长，不是常数时间或发布性能保证；未验证大历史极端负载。此增量不修复此前已经发生的身份复用或 §9 之前漏记 ID 的重做历史，不自动改写工程。实际原生进程崩溃、断电与设备验收另行记录。

## 11. 轨道与片段颜色（UI 元数据字段）

状态：2026-10-24 实施前建立（来源：[P2 工作区契约 §6.2](p2-daw-workspace.md) 的模型触点决策）。颜色是权威工程模型的 UI 元数据：进入统一事务、参与撤销/重做，但不影响任何 MIDI 语义。

- 字段：`Track.Color`、`Clip.Color`，字符串，`json:"color,omitempty"`。空串 = 未设置（合法值）。
- 调色板枚举（仅此 8 个非空值）：`ice`、`mint`、`violet`、`amber`、`coral`、`rose`、`slate`、`moss`。调色板随模型常量内置；越界值在操作校验层拒绝（track.add/track.update/clip.add/clip.update 与 TrackSpec/TrackPatch/ClipSpec/ClipPatch 同步携带）。
- 默认与回退：**模型不自动分配颜色**——track.add / clip.add / SMF 导入均为空串。显示层对空值按轨道序在调色板上取回退色（当前产品没有重排轨道的操作，序号稳定）；用户显式设置后持久保存。理由：自动分配会让导入/重放的取值依赖应用时点，且与 §9/§10 的服务分配值持久化机制耦合，收益只有默认观感。
- MIDI 边界：SMF 导出不写出颜色；SMF 导入忽略外部颜色（MIDI 无此语义），一律为空。颜色不参与音符配对、事件排序、导出排序或播放编译。
- 事务与撤销：颜色修改走 `track.update` / `clip.update` 补丁字段，与名称/通道等同样冻结修订、整组原子、逆操作恢复原值；`track.remove` / `clip.remove` 的逆操作携带删除时的颜色，恢复不丢。
- 不新增 schema 版本：颜色是工程状态 JSON 的可选字段，旧工程缺省读为空串；无迁移、无旧格式读取路径。
- 验收：调色板内合法往返与撤销/重做；越界值拒绝且无部分变更；导入/导出不携带颜色；remove→undo 恢复原色；GUI 与 Agent 同一命令路径。
