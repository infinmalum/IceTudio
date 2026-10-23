> 历史实现参考，不是删除授权。当前范围由 [P0 执行契约](p0-refactor-contract.md)、[逐项清单](p0-api-storage-inventory.md)及[实施计划](../IMPLEMENTATION_PLAN.md)规定。四套通用场景模板删除；示例涉及的调度、审查、权限与恢复能力保留，测试改用新配置。

# 默认场景使用与验收

四个模板均为可编辑副本。日常、写作使用直接执行及按需审查；研究、开发使用动态规划、worker 和必需审查。模板都未取得真实模型稳定验证标记。没有必需安装的 Skill；可按需要选择自己的 Skill/MCP。模板不会授予本机权限。

桌面先配置模型，打开场景模板的“使用说明”，创建副本，再开始对话。文件操作在当前对话设置工作区权限；命令执行还需允许相应可执行文件。远端模型需凭据和数据外发授权。联网在对话框开启，不保存在 Profile。审批、取消、结果审查、过程复盘都留在当前场景内。必需审查最多返工两轮，共享原任务预算；工具缺失或结果未知不能当作完成。

## 场景案例

| 模板 | 输入与案例 | 交付及验收要求 | 工具准备 |
|---|---|---|---|
| daily | 把“小林周五交稿，小周审核，发布时间未定”整理成三项待办，附负责人与期限 | 保留已知负责人/期限；未知项标待确认；纯文本任务无需 planner/worker/自动审查 | 无；文件任务才需 local 与工作区权限 |
| writing | 把“周六10点维护，问题联系support@example.com”改为面向新用户的两段通知，亲切且不超过120字，不加承诺 | 两段可直接使用的正文；保留时间和联系方式，不补造承诺；需要时单次审查 | 默认不启用工具；文件编辑需选择 local 并授权 |
| research | 只比较[A]20元/30天、[B]15元/保留期限未说明，引用[A]/[B]，不要联网 | 按价格/保留期限比较；未知项明确；不编造出处；分析任务完成后汇总，通过独立审查 | 对话材料即可；委派需审批。本机材料需读取权限，外部检索需联网开关或配置 MCP |
| development | 在获准工作区修复calc.py中add误用减法，只改该文件，运行python3 -m unittest | 实际读取与修改；真实检查结果及修改文件；未运行/失败/未知检查不能宣称通过；独立审查 | local 内置读写编辑/exec 即可；工作区与python3执行权限，委派/写入/命令审批 |

开发案例需先提供带失败测试的项目。必要工具检查可以在高级配置中指定 `local__exec`；宿主只接受真实成功调用且结果为 `passed:true`。这表示指定命令成功，不能证明模型选对了测试，因此仍应明确验收命令。exec 为本机执行，不提供操作系统沙箱。研究材料不足时需等待用户补充；开发工具不可用时需明确阻塞。

## CLI 路径

桌面或 `bin/icepick serve` 必须已启动；以下每行分别执行。`PROFILE_ID`、`CONVERSATION_ID`、`RUN_ID` 替换成命令返回的实际 ID。

```sh
bin/icepick templates list
bin/icepick templates show development
bin/icepick templates create development
bin/icepick conversations create --profile PROFILE_ID
bin/icepick conversations permissions CONVERSATION_ID --mode semi --workspace /实际项目路径 --command /usr/bin/python3
bin/icepick conversations start CONVERSATION_ID "修复calc.py中add的减法错误，只修改该文件，用/usr/bin/python3 -m unittest验证。" --wait
bin/icepick approvals list
bin/icepick approvals approve CALL_ID
bin/icepick tasks status RUN_ID
bin/icepick conversations show CONVERSATION_ID
bin/icepick tasks review CONVERSATION_ID RUN_ID
bin/icepick tasks retrospect CONVERSATION_ID RUN_ID
```

`--wait` 不自动批准调用；遇到审批时按输出指引处理，在另一个终端或桌面查看任务。日常/写作/研究路径只需更换模板 ID 和任务文本。切换场景不改变对话的上下文归属；需要隔离任务资料时创建新对话。

## 已验证范围

2026-10-14：模拟模型通过真实宿主验证四个模板创建与执行、直接场景无自动审查、动态场景必需审查、研究缺材料进入等待；开发模拟模型驱动真实本机精确编辑与命令，保留工具证据并通过宿主检查门槛。CLI 通过真实 IPC 验证模板说明与日常/写作任务路径；桌面通过浏览器模拟服务验证模板说明和已有完整对话交互。相关权限、审批拒绝、命令失败、取消、预算和并行依赖沿用现有边界测试。

模拟模型返回的正文仅验证协议与记录路径，不证明真实模型能遵守文风、引用、规划或测试选择。发布稳定模板前仍需用上述案例重复调用真实支持模型，记录模型/模板/组件版本、工具与 Skill 指纹、结果、预算及失败路径；人工桌面和干净用户安装验收尚待完成。
