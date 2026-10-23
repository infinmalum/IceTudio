> 历史实现参考，不是删除授权。当前范围由 [P0 执行契约](p0-refactor-contract.md)、[逐项清单](p0-api-storage-inventory.md)及[实施计划](../IMPLEMENTATION_PLAN.md)规定。只删除 Profile 草案产品；Skill 草案、improver、共享模型调用和证据逻辑保留。

# Profile / Skill 草案

在当前对话下，通过“AI 配置草案”输入工作方式要求。默认生成新 Profile；勾选“改进当前编辑的 Profile”时，以编辑器选中的已保存配置为基础。没有保存的编辑不会作为基础。生成使用当前对话 Profile 的 improver 模型（未指定时使用公共或宿主模型），并沿用该对话的外发授权；目标 Profile 的模型不会用于生成。

在高级组件配置勾选“允许显式生成配置草案”并保存，才能使用此入口。四个最新场景模板默认开启，空白默认关闭；此前创建的副本可自行开启或恢复出厂内容。生成只调用一次模型，不隐式读取历史、记忆或其他任务；可显式指定当前上下文已结束任务作为证据。Skill 草案还可明确选择一个源 Skill 的 SKILL.md 内容。不执行工具、联网或委派。生成需求与基础配置可能包含私人信息，远端发送仍需相应授权。

候选首先保存为草案，不修改 Profile。查看原始需求、变更说明、限制、已保存差异及引用影响；需要时编辑草案 JSON，再“校验并保存草案”。编辑未保存时不能采用，避免界面与实际发布内容不一致。手动采用新建草案创建独立配置，不切换对话；采用改进草案发布目标新修订，仅影响后续任务，在途任务保留快照。静态/资源校验不代表真实场景验收，采用前仍需核对依赖和可分享内容是否含私人资料。

每份草案保留目标的基础修订。草案已编辑、处理，或目标已修改/删除时，旧修订不能采用；不自动合并覆盖人工改动。已采用的改进可以“撤销此配置改动”：发布记录的基础配置为新修订；若采用后有其他编辑，撤销会拒绝覆盖。新建配置没有原版本，使用配置库删除即可。丢弃的草案保留记录，不能再采用。

CLI 使用同一宿主服务，以下命令中的 ID 替换成返回的实际值，每行分别执行：

```sh
bin/icepick profiles draft CONVERSATION_ID "创建一个面向初学者的解释助手，直接执行，不使用工具"
bin/icepick profiles draft CONVERSATION_ID "解释时先举例，再说明概念" --target PROFILE_ID
bin/icepick profiles drafts list
bin/icepick profiles drafts show DRAFT_ID
bin/icepick profiles drafts edit DRAFT_ID proposal.json --revision 1
bin/icepick profiles drafts apply DRAFT_ID --revision 2
bin/icepick profiles drafts revert DRAFT_ID --revision 3
bin/icepick profiles drafts discard DRAFT_ID --revision 1
```

`proposal.json` 只包含草案的 `proposal` 对象（name/data/summary/reasons/limitations），不包含宿主生成的 ID、状态和基础修订。每次操作使用刚查看的草案修订；命令中的数字仅演示生成→编辑→采用→撤销顺序。`profiles draft` 返回任务 ID；可用 `tasks status` 查看或 `tasks cancel` 取消，完成后刷新草案列表。

Profile 和 Skill 均可通过 `--evidence RUN_ID,RUN_ID`（桌面填写任务ID）明确选择最多3个当前上下文已结束根任务。失败、取消、中断任务也可选择，所有子组件必须结束。没有选择则不读取任何任务过程；证据超过512KiB明确拒绝。保存过程指纹和真实记录引用；任务记录变化后拒绝生成落盘或采用，需重新生成。expectedChecks 是后续可执行的验收要求，当前不执行候选评估。

## Skill 草案

在“AI Skill 草案”输入需求，源 Skill 名称留空表示新建，填写名称则读取该资源的单个 SKILL.md。查看源内容与候选、源引用配置及全部 Skills 配置影响，再编辑/校验/采用。采用发布唯一副本名，不覆盖源文件，也不切换显式引用；需要使用时在 Profile 的 Skills 列表选择副本名。选择“全部 Skills”的配置在后续运行中可见新副本。

副本在宿主数据库中不可变保存，仍通过标准 Skill 发现、按需加载与任务快照使用。重启恢复已发布副本；分享包包含标准 SKILL.md，不附草案需求、任务证据或本机授权。只支持单个 SKILL.md，不生成或运行配套脚本/资源。撤销副本不影响历史或在途快照；仍有 Profile 显式引用副本时，需要先取消引用。

```sh
bin/icepick skill-drafts generate CONVERSATION_ID "提炼可复用的复核方法" --evidence RUN_ID
bin/icepick skill-drafts generate CONVERSATION_ID "加入未知结果处理" --source SKILL_NAME
bin/icepick skill-drafts list
bin/icepick skill-drafts show DRAFT_ID
bin/icepick skill-drafts edit DRAFT_ID proposal.json --revision 1
bin/icepick skill-drafts apply DRAFT_ID --revision 2
bin/icepick skill-drafts withdraw DRAFT_ID --revision 3
bin/icepick skill-drafts discard DRAFT_ID --revision 1
```

Skill 的 proposal.json 只含 content/summary/reasons/limitations/expectedChecks/evidenceRefs。采用与撤销必须使用刚查看的草案修订；示例数字不是固定值。

来源限制会保守继承到派生 Profile/Skill，未来复用、复制和修订也不能自动降低外发范围。分享导出前，桌面要求检查已知派生内容，CLI 先显示来源检查，用 `profiles export PROFILE_ID --reviewed` 保存核对后的包。检查提示不能证明内容已脱敏，请查看正文是否含任务资料、凭据或其他不应分享的内容。

本批不执行候选场景评估，也没有自动修改、自动采用或新旧配置运行对照。数据库格式为 v26，旧版本明确拒绝且保留文件；不提供迁移或旧格式读取。分享格式仍为 v6。
