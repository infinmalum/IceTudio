> 历史实现参考，不是删除授权。当前范围由 [P0 执行契约](p0-refactor-contract.md)、[逐项清单](p0-api-storage-inventory.md)及[实施计划](../IMPLEMENTATION_PLAN.md)规定。材料、附件及来源权限保留；旧产品配置引用按新契约适配。

# 对话材料：附件、网页与本地目录

直接在输入框旁点“添加附件”，支持 PNG/JPEG/GIF/WebP、PDF、UTF-8 文本和 DOCX。可只附文件后发送，也可说明要分析什么。发送前可以移除附件，发送失败保留待发送文件；切换对话清空输入草稿。Profile 的 ZIP 分享包请使用“Profile 管理”中的“导入 Profile / 导出 Profile”，不作为对话附件。

每次最多8个文件，单个10MiB，总计20MiB。文本和 DOCX 提取正文限256KiB。DOCX 提取段落文本，不处理嵌入图片、批注、页眉和版式；需要视觉内容时单独附图或转 PDF。旧二进制 DOC、任意 ZIP、音视频不支持。无效、空文件和超限文件返回明确错误，不静默截断。

附件在发送任务时与消息及来源范围一同持久化，保留名称、原始大小与 SHA-256；文本保存提取结果，图片/PDF 保存原始二进制的 base64。消息列表只返回文件元数据。后续轮次、子任务、规划、审查和复盘读取已保存内容；会话压缩时材料参与摘要，原始消息材料仍留在数据库。审查/复盘证据指纹包含附件元数据。附件不在 Profile 分享包里，外发继续遵守 personal/unknown 授权、任务冻结上限和当前权限。

OpenAI 普通聊天与兼容接口使用内容块，联网模式使用 Responses input_image/input_file；Anthropic 使用 image/document 块；Gemini 使用 inlineData 或 Interactions image/document。是否理解图片/PDF 取决于所选模型和端点，服务错误会显示，不静默改成只有文件名的文本请求。使用纯文本模型时可上传文本或 DOCX 提取结果。

## 网页读取

开启已有“联网搜索”，把 URL 直接粘贴到消息中，无需新的按钮或 Profile 设置。模型可以按需读取指定网页，再据正文作答：

- OpenAI：Responses web_search 的 open_page 动作。
- Anthropic：web_fetch_20260318，与 web_search 共用预留调用额度；总额度至少2时分配给两种工具，只有1次额度时按当前用户消息是否含 URL 选择。只允许 direct，不启用云端代码执行。
- Gemini：Interactions url_context 与 google_search 共存，保存访问 URL 及 success/error/paywall/unsafe 状态。

开关关闭不声明这些服务器工具；打开不等于每轮强制联网，也不绕过外发授权。搜索记录展示 URL、状态、引用和用量。读取失败或页面受限时不能把摘要冒充全文。OpenAI/Anthropic 每请求共享至多3次联网调用，Gemini 按宿主请求计数，内部实际调用量和费用未报告时仍为未知。planner、reviewer、摘要器不主动联网。MCP 模式继续使用用户已经配置的能力，不假设任意搜索 MCP 都能获取全文。

## 本地路径

把目录或文件的绝对路径写到消息中即可。原有 `local__read_file` 现在也能列目录：最多200项、报告截断、不自动递归。模型可再按需读取相关子路径，每次执行仍经过原有工作区权限检查。文本文件读取限64KiB；大型目录可用已授权的本机命令检索。指定路径不自动扩大全局访问权限，也不把整个目录上传。

## Skill 配套资源

任务开始时同时冻结 SKILL.md 和 `references/`、`scripts/`、`assets/` 下的常规文件。默认 `local__load_skill({"name":"…"})` 返回指令及资源清单；传 `path`（例如 `references/guide.md`）读取该任务的冻结版本。文本返回正文，二进制返回标明 base64 编码及指纹的 JSON。每个资源限1MiB，每个 Skill 最多128个、资源合计8MiB，整个任务的依赖内容仍限16MiB。

资源路径必须匹配清单，拒绝符号链接、特殊文件、绝对路径和路径逃逸。原始 Skill 被修改、移动或删除不影响已经启动的任务。资源修改会改变草案源指纹；发布副本保留源资源，分享包含这些资源。加载脚本只读取冻结内容；要执行时使用现有 write_file 将该内容写到授权工作区，再用 exec 运行，沿用命令审批，不直接执行可能已经变化的源文件。

协议参考：[OpenAI 文件输入](https://developers.openai.com/api/docs/guides/file-inputs)、[OpenAI 网页工具](https://developers.openai.com/api/docs/guides/tools-web-search)、[Anthropic Web Fetch](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool)、[Gemini URL Context](https://ai.google.dev/gemini-api/docs/url-context)。本批验证使用本机模拟 HTTP/SSE 服务和浏览器模拟宿主，不代表已完成真实模型质量验收。
