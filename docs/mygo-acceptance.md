# MyGo 迁移收尾与验收范围

日期：2026-11-04。对象：维护者及后续实现者。本文记录当前实现与已执行验证；迁移设计及 A1–A10 门槛仍以 [迁移规范](mygo-migration.md) 为准，工程/授权/恢复约束仍以 P0–P4 契约为准。

## 当前交付

正式入口 `main.go` 使用 MyGo v0.7.4，携带参数仍走原 CLI；无参数打开实际工作区。Native 实现工作区、Browser、Arrangement、轨道/片段属性、Transport、时间图、音频设置、Agent 输入/历史/Markdown、审批/候选预览、任务详情、模型/权限/MCP/Skill/执行配置。音乐操作经桌面 Edits / app.Service 使用原事务、修订、操作 ID、共同撤销与恢复；没有重写 Agent 调度或循环。

钢琴卷帘仍使用原 GridSound 子集与 React hook，通过独立页面及最小桥接接入；普通 React App/Arrangement 已退役。PDF/GIF 与供应商搜索建议 HTML 的只读正文是已记录的 WebView 例外，普通操作仍 Native。

Wails Go/npm 运行依赖、生成绑定、旧窗口/事件/关闭握手、旧普通 React 源码及框架专属打包脚手架已移除。Task、CI、开发启动与平台打包入口改为直接 Go/MyGo 构建。原测试断言及旧关闭载体在 `tests/retired-react` 保留为审计参考，活动测试驱动改为 Native。2026-11-04 维护者针对自动审查意见明确批准此退役方案；无需再次申请同一范围的清理许可。

保留工程数据、旧浏览器 profile、SQLite、CLI/IPC、音频链路与现有 Agent 执行能力。Linux 旧文字/IndexedDB 附件通过只读 profile 快照读取器显式接管，冲突不覆盖、相同导入不重复。Windows/macOS 自动 profile 接管未验收；可导入明确导出的恢复文件，原 profile 不删除。

## 已执行验证

以下均在本机 Linux、已有依赖及实际桌面环境中运行；不等于 CI 远端已执行。

| 检查 | 结果与范围 |
|---|---|
| `go test ./...` | 通过；包括保留的服务、Agent、权限、存储、工程、音频与新增 Native 测试，默认跳过需桌面的 opt-in 测试 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/desktop ./internal/app` | 通过；不代表全部平台或音频实时行为 |
| `npm --prefix frontend run build` | 通过；只构建独立卷帘 |
| `npm --prefix frontend run test:music` | 15 项通过，含共享小节尺样例、投影与 GM 数据 |
| 实际 MyGo/WebKit 回归 | 通过只读 SQLite 工程、Native 选区→卷帘 DOM 切换；新建/属性/时间图/SMF 往返、卷帘编辑、共同撤销、丢失确认后查询原结果不重放、退出取消 |
| 实际 Agent UI 路径 | Native 发送→本机 HTTP 模型夹具→差异/ghost→批准原调用→真实音乐事务→撤销通过；不是外部供应商连通性验证 |
| 密集音符 | 实际卷帘 1000/10000 音符全选（含视口外 ID）、移调及 Native 撤销通过；64 轨绘制已采集。测得耗时仅为单次观察，不是 frame p95 |
| 只读正文 | 实际 Untrusted WebView 调用 Go 绑定被拒绝，sandbox iframe 脚本不能修改父页；不是完整恶意内容/导航矩阵 |
| 旧草稿 | 实际 WebKit profile 的文字与 IndexedDB File 导出通过，源文件字节不变；Native 导入冲突/幂等测试通过 |
| 正式入口与便携包 | 无参数启动后进程保持运行且在一次性指定目录创建 SQLite；包内二进制哈希与正式产物一致、许可证存在、无应用数据库。这不是物理文件管理器双击/完整安装验收 |
| 构建配置 | Taskfile/CI/nfpm YAML 解析通过，产品/卷帘源码无旧绑定导入；平台脚本仍需对应工具运行 |

最终窗口证据保存在 [final](mygo-evidence/final/result.json)，含独立 Native 与卷帘截图、设置/审批截图、64 轨截图和 dense.json。Native CapturePage 在嵌入 WebView 位置出现黑洞，卷帘另行 CapturePage；二者不是物理桌面合成截图。测试只使用临时数据、本机模型夹具，不使用日常工程。夹具启用的编辑探针将原测试工程 revision 增加 8，音乐与 CC 保持不变；不得对日常数据运行 smoke-edit。

## 构建及打包

在仓库根目录，已有依赖时依次运行 `npm --prefix frontend run build`、`go build -o bin/icetudio .`。启动 `./bin/icetudio`；开发可用 `node scripts/dev.mjs`，构建后从稳定 bin 目录启动，避免数据放在 go run 的临时目录。

Linux 便携包使用 `python3 scripts/package.py --os linux --binary bin/icetudio`，包含可执行文件、项目许可证及第三方通知；仅复制固定哈希符合要求的 GM 音色，不复制用户工程。系统 GTK3、WebKitGTK 4.1、FluidSynth 2.6+ 仍是运行前提，缺少音色时使用既有 Basic Waves 兜底。生产程序默认沿用二进制旁 data，可显式设置 ICETUDIO_DATA_DIR；便携包启动位置变化不会自动移动已有数据。

Windows/macOS、NSIS/MSIX、DEB/RPM/Arch、AppImage 与签名任务已替换框架命令，未在本机实际制作/安装。对应平台必须具备其 SDK、打包和签名工具后再执行；不能将 YAML/脚本检查当安装验收。当前本机未安装 Task/nfpm/NSIS/AppImage 工具，未为本次收尾下载新工具。完整发行许可审计仍需针对实际分发内容进行。

## 尚未验收及后续门槛

物理 IME、不同 DPI、触控/窗口外释放、音频设备故障/实际听感和延迟、真实供应商模型/MCP/搜索、完整退出/崩溃矩阵、frame-p95 与干净系统/跨平台安装仍未完成验收。上述条目不是已知全部失败，也没有被本次清理授权豁免。框架替换与代码清理完成，不等于 M0–M6/A1–A10 全项验收完成。

旧忽略输出 frontend/dist 已删除；此前自动审批连接故障导致的未执行动作，经重新提交同一已授权清理范围后成功执行。Wails 历史文档和退役断言中的名称是研究/审计记录，不是产品运行依赖。

DAW E0–E8 实现继续暂停，待维护者按迁移门槛确认后再恢复。完整 DAW 编辑器及验收完成后，仍须维护者确认才能重新评估 ADK/Agent 循环替换；不能因清理 Wails 提前启动。
