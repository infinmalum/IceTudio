# E1 选中音符变换

2026-11-01，实现规格。范围：钢琴卷帘选中音符的量化、±1/±12 半音移调。不连接模型可用。

新增 app/Wails 只读接口 PrepareNoteTransform(projectID,baseRevision,clipID,noteIDs,spec)，仅在当前修订与基准一致、所有选择 ID 属于目标片段时调用既有 project.CompileTransform。空选择、重复/消失 ID、消失片段拒绝。GUI 移调要求整组不越界；拒绝部分音符被跳过的结果。原 Agent 变换的跳过规则保持原契约。

接口不写音乐；返回既有 note.update ops。GUI 在点击时冻结工程/修订/片段/选择/参数，编译后经 useProjects.edit 提交；发送前写恢复标记，统一 SubmitProjectOps/撤销/结果查询。编译期间后续修改使最终提交冲突，不重新计算或静默换基准。无新表、无独立历史、无 Agent 工具或 CLI 写入口。

量化单位为 1/4、1/8、1/16、1/32、1/8T、1/16T，强度固定 100%，只改变起点；界面单位选择是工具偏好而非待应用工程表单。移调是即时动作，无需保留数字输入草稿。量化不自动扩展片段，若结果超出当前片段则由事务校验拒绝整组。全部无变化时显示原编译器原因。

KEEP：非选中音符、力度/时长/通道、CC/raw、工程恢复、权限与所有原有入口。变更调用链：PianoRoll → usePianoRoll → Wails PrepareNoteTransform → app → project.CompileTransform；写入仍走 props.edit → useProjects.mutate。生成绑定由已有 Wails CLI 生成，不手写绑定。

验收：服务测试核对只读/精确选择/量化与移调共用计算、越界/未知 ID/旧修订拒绝、编译后并发提交冲突和共同撤销；真实工程浏览器核对选择→工具→持久工程→撤销。构建不替代原生窗口验收。
