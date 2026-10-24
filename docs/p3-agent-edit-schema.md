# P3 Agent 音乐工具 Schema 契约（project__edit / project__query）

状态：规格定稿（2026-10-23）。来源为评审发现：`project__edit` 暴露给模型的 ops.items 仅有 `kind`（internal/app/project.go），而后端 `internal/project.Op` 已有完整结构与校验。本契约以 `internal/project/ops.go`、`internal/project/model.go` 当前实现为准；实现时若 Op 契约已变，以变更后的实现为准并同步本文。

## 1. 目标与不变量

- 模型可见 JSON Schema 与 Op 真实契约一致：每类操作有明确字段、必填项和数值范围，模型不靠猜测 `notes`/`noteDeltas`/`trackPatch`/`clipPatch` 等字段名；
- 采用按 `kind` 判别的 oneOf（discriminated union）结构；
- **不暴露 inverse-only 字段**：`InvClips`/`InvNotes`/`InvControls`/`InvRaw` 仅由 `ApplyNewTransaction` 的求逆过程填充，客户端载荷非空即被拒绝（P1 §10 已验证），schema 不得声明它们；
- **后端校验仍是最终权威**：对象 ID 唯一性、片段范围适配（clipFitCheck）、轨数/音符数上限（MaxTracks=64、MaxNotes=200000）、tempo/meter 首点必须在 tick 0 等跨对象约束不进入 schema，由服务端拒绝；schema 收紧不替代、不削弱任何服务端校验；
- 不改变 Op 解码语义、事务语义与 GUI/CLI 行为；本项是纯 Agent-facing 声明与解码边界的收紧。

## 2. 顶层参数（不变）

`projectId`（string，必填）、`opId`（string，必填）、`baseRevision`（integer，必填）、`ops`（array，必填）；顶层 `additionalProperties: false` 维持现状。

## 3. ops 的 oneOf 结构（13 类 kind）

ID 字段（客户端提供的可选 id）统一约束：`^[a-z0-9_]{1,64}$`。

| kind | 载荷字段（类型/范围） | 必填 |
|---|---|---|
| project.rename | title: string（≤200 字符） | title |
| tempo.set | tempo: [{tick: int 0–2^31−1, usPerQuarter: int 1–16777215}]，minItems 1 | tempo |
| meter.set | meter: [{tick: int 0–2^31−1, numerator: int 1–255, denominator: enum [1,2,4,8,16,32,64,128]}]，minItems 1 | meter |
| track.add | track: {id?, name?, color?, channel: int 0–15, program: int 0–127, muted?, solo?}；color 为调色板枚举或空（见 P1 §11） | track |
| track.update | trackId: string；trackPatch: {name?, color?, channel?, program?, muted?, solo?}，至少一个字段；color 须为调色板枚举或空 | trackId, trackPatch |
| track.remove | trackId: string | trackId |
| clip.add | clip: {id?, trackId, name?, color?, start: int ≥0, length: int ≥1} | clip |
| clip.update | clipId: string；clipPatch: {name?, color?, start?, length?}，至少一个字段 | clipId, clipPatch |
| clip.remove | clipId: string | clipId |
| note.add | clipId: string；notes: [{id?, start: int ≥0, duration: int ≥1, pitch: int 0–127, velocity: int 1–127, channel: int 0–15}]，minItems 1 | clipId, notes |
| note.update | clipId: string；noteDeltas: [{id, start?, duration? ≥1, pitch? 0–127, velocity? 1–127}]，minItems 1；**无 channel——通道不可经 note.update 修改** | clipId, noteDeltas |
| note.remove | clipId: string；noteIds: string 数组，minItems 1 | clipId, noteIds |
| control.add | clipId: string；events: [{id?, tick: int ≥0, kind: enum [cc, pitchBend, program, channelAftertouch, polyAftertouch], channel: int 0–15, + 按 kind 的字段见下}]，minItems 1 | clipId, events |
| control.remove | clipId: string；eventIds: string 数组，minItems 1 | clipId, eventIds |

control.add 的 events 按 `kind` 细分（同层 oneOf）：

- cc：controller: int 0–127，value: int 0–127（均必填）；
- pitchBend：value: int 0–16383（必填）；
- program：program: int 0–127（必填）；
- channelAftertouch：value: int 0–127（必填）；
- polyAftertouch：note: int 0–127，value: int 0–127（均必填）。

schema 无法表达的约束（由后端拒绝，description 中提示）：音符/事件不得超出片段长度、tempo/meter 表必须以 tick 0 开始、同 tick 重复点取末值、同批内 ID 唯一。

## 4. 解码边界收紧

`decodeRequiredOperation` 对 `project__edit` 的 ops 元素增加未知字段拒绝（`DisallowUnknownFields` 或等价机制）：模型拼错字段名（如 `note` 而非 `notes`）当前会被 `json.Unmarshal` 静默忽略并落到"缺少要添加的音符"类间接错误；收紧后必须给出明确错误并列出该 kind 的合法字段。GUI/CLI 的同一解码入口如受影响，须保持其行为不回归（它们本就不发送未知字段）。

## 5. 工具 description 使用规则（补充文案）

在现有"baseRevision 来自最近一次查询、冲突必须重新查询后重试"基础上补充：

- ops 引用的对象 ID 必须来自最近一次查询结果，不得凭空构造；
- 范围不明确时先 `project__query` 限定区域查询，再构造批量编辑；
- 不得自行重放未确认结果；中断后先查询提交结果（重放由 opId 幂等机制安全处理，但模型不得在未查询时重复提交大批次）。

## 6. 测试要求

- schema 静态一致性测试：解析 tool definitions 的 JSON Schema，断言 13 类 kind 全部覆盖、数值范围与 `ops.go`/`model.go` 校验常量一致（常量以单一来源引用或镜像断言，防止两处漂移）；
- 正例：每类 kind 至少一个"经 schema 校验通过 → 后端接受"的完整操作往返（模拟模型构造合法调用）；
- 负例：错字段名（解码边界拒绝）、越界值（pitch 128 / velocity 0 / channel 16）、出现 inverse-only 字段、空载荷，均被 schema 或解码边界明确拒绝且不产生工程变更。

## 7. 边界

- 本项不实现 P3 的选区冻结、范围限定任务上下文、可视差异、候选试听与采用/丢弃（另行规格，见 [P3 计划节](../IMPLEMENTATION_PLAN.md)）；
- 大 oneOf 对不同模型 provider 的 schema 支持度存在差异；如某 provider 校验失败，回退策略不得是放宽 inverse-only 或删除数值范围，只能作为兼容性问题单独处理并记录；
- `project__query` 的 schema 已满足需求（范围参数已声明），本契约只约束 `project__edit`；query 结果 JSON 即 ops 载荷字段形状的参照，两者字段命名保持一致。
