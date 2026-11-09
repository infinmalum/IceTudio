# IceTudio

IceTudio 是一款开发中的 **AI native 音乐创作应用**。音乐工程是工作中心：你可以离线编辑 MIDI，也可以让 Agent 修改选定轨道或片段。手工操作与 Agent 共用 Go 工程服务、修订检查和撤销历史，对话与音乐工程分别保存。

## 当前状态

桌面代码于 2026-11-04 从 Wails 切换到 MyGo，当前锁定 **MyGo v0.8.3**。普通工作区、Arrangement、Agent 和设置采用 Native UI；钢琴卷帘继续在嵌入式 WebView 中运行原 GridSound 子集。Wails 运行依赖、生成绑定和旧普通 React 外壳已移除，不再需要 Wails CLI。

当前支持 MIDI 创作与基础音频制作：

- 工程新建、重命名、删除、持久保存及标准 MIDI 导入/导出。
- Native 多轨编排、片段选择/移动/末端拉伸、缩放、静音/独奏、颜色与音色属性。
- 钢琴卷帘音符编辑、量化、移调、力度调整，以及速度/拍号变化图和小节尺。
- 连续编排/卷帘导航、跨视口选择、复制粘贴、音乐时间定位、三连音/自适应吸附、播放跟随和可恢复工作区布局。E1 代码与自动化闭环已完成，人工/设备门槛见 [E1 交付记录](docs/daw-expansion/e1-acceptance.md)。
- E2 已接入共享 Pattern、分组编排、多音色鼓组、卷帘控制通道与音阶、离线和弦试听、节拍器和预备拍。真实窗口与离线 PCM 已回归，物理设备验收见记录。[E2 实施记录](docs/daw-expansion/e2-midi.md)
- Linux FluidSynth 播放、定位/循环、乐器试听、本地 SF2/SF3 管理。
- E3 提供 WAV/FLAC 导入与波形片段、音频/MIDI take、混音或指定轨导出、以及可携带工程资源包。[E3 实现与验收记录](docs/daw-expansion/e3-audio.md)
- E4 的原生混音台提供总线、发送、压缩侧链、内置效果、延迟补偿和自动化。[E4 实施记录](docs/daw-expansion/e4-mixer.md)
- E5 提供本地素材索引与预听、复制或引用、采样器与鼓垫、瞬态切片、非破坏性拉伸、稳定单声部音高校正和 Take Comp。[E5 实施记录](docs/daw-expansion/e5-sampling.md)
- E6 已增加 Linux 内置效果和 Native 插件库。插件库可隔离扫描本地 CLAP/VST3 并重试失败模块。第三方插件的轨道和混音槽加载仍在开发。[E6 执行记录](docs/daw-expansion/e6-plugins.md)
- Agent 对话、附件、发送范围确认、工具审批、候选差异/只读 ghost、原版与候选试听，以及共享撤销。
- 模型与凭据配置、权限与外发授权、MCP、搜索、反馈驱动的 Skill 草案和版本管理、任务详情及内部执行配置。
- 未应用输入检查点、退出保护和中断后按原操作 ID 查询实际提交结果，不自动重放未知操作。

**代码迁移完成不等于全部产品验收完成。** 本机已通过 Go 测试、race、vet、音乐规则测试和实际 MyGo/WebKit 回归，包含本地模型夹具审批与 1k/10k 音符批量编辑。物理音频/IME/DPI、完整故障矩阵、真实供应商连接及干净系统/跨平台安装仍待验收，范围见[迁移收尾记录](docs/mygo-acceptance.md)。

[DAW 扩展计划](docs/daw-expansion/README.md)记录后续的插件宿主、创作工具及跨平台交付。执行顺序为 **MyGo 迁移验收 → 完整 DAW 编辑器 → Agent 框架评估**。维护者于 2026-11-05 恢复编辑器开发，Linux 主体功能已实现至 E5，E6 正在进行。迁移的设备和跨平台验收仍单独追踪，Agent 执行内核继续冻结。

## 架构

```text
MyGo Native 工作区 / Arrangement / Agent
                  │
WebView Piano Roll ── 桌面编辑协调与桥接
                  │
  独立内部 IPC ───┤
              app.Service
                  │
      工程事务 / SQLite / Playback / Agent
```

Go 核心持有权威工程，UI 仅保存显示投影和临时手势状态。编辑冻结对象与基础修订，冲突拒绝覆盖；工程、编辑历史与对话历史相互独立。保留内部角色、委派、任务图、审查/复盘、预算、权限和恢复能力。

React、TypeScript 和 npm 仅用于卷帘构建，不需要 Node 产品运行进程。普通 Agent Markdown、表格、代码及文本/静态图片附件使用 Native；PDF/GIF 和供应商搜索建议 HTML 使用受限的只读正文 WebView，输入与操作仍为 Native。

详见[架构](docs/architecture.md)、[迁移规范](docs/mygo-migration.md)与[门面调用定位](docs/mygo-inventory.md)。

## 构建与运行

当前实际验证平台为 Linux。构建需要 Go **1.27.3+**、Node.js **22.18+** 和 npm，以及 cgo/C 编译工具链。音乐测试使用 Node 内置 TypeScript 类型剥离；本机验证版本为 Node 26.13.0。

| 平台 | 依赖与验证范围 |
|---|---|
| Linux | GTK3、WebKitGTK 4.1；FluidSynth **2.6+** 和 ALSA 开发头文件与动态库。音频导入、FLAC 和变采样率导出使用本机 FFmpeg。音频精修需要其 Rubber Band 滤镜，录音和预听使用 PipeWire 工具。已有本机构建和实际窗口验证 |
| Windows | WebView2 runtime；安装包、音频及整个平台尚未验收 |
| macOS | 系统 WebKit；安装包、音频及整个平台尚未验收 |

MyGo 不使用 cgo，并不意味着本项目可关闭 cgo：SQLite 和 Linux 音频仍依赖它。没有可用音频引擎时应明确报错，音乐编辑无需连接模型或外部 MCP。

以下命令在仓库根目录执行。首次准备 npm 依赖（会下载软件包，Agent 执行前须取得明确许可）：

```sh
npm --prefix frontend ci
```

先构建卷帘资源，再构建桌面程序；首次 Go 构建也可能下载模块，需要网络及相应许可：

```sh
npm --prefix frontend run build
go build -o bin/icetudio .
```

启动 Linux 正式工作区：

```sh
./bin/icetudio
```

桌面版直接打开工作区。原 IceTudio 命令行客户端已移除；内部 IPC 集成可单独构建 `go build -o bin/icetudio-server ./cmd/icetudio-server`，使用相同数据目录时须先退出桌面版。IPC 宿主不随桌面版打包。

开发命令 `node scripts/dev.mjs` 会构建到稳定的 `bin/` 目录后启动；已安装 Task 时也可使用 `task build`、`task dev`。Windows 构建入口为 `task windows:build`，生成 `bin/icetudio.exe`；平台任务的实际安装验收状态见收尾记录。

Linux 插件库扫描需要与主程序同目录的 `icetudio-plugin-scan`，以及系统的 Bubblewrap 和 `prlimit`。[扫描助手构建与使用](docs/daw-expansion/e6-plugins.md#linux-native-插件库)

Linux 便携包使用标准库脚本生成，不自动安装系统运行库：

```sh
python3 scripts/package.py --os linux --binary bin/icetudio
```

输出位于 `bin/packages/`。其他安装器与签名入口需要对应平台 SDK 和打包工具，本机尚未实际制作/安装，不因脚本存在而视为发布完成。

## 数据、音色与恢复

直接运行桌面二进制默认使用其旁边的 `data/`；`ICETUDIO_DATA_DIR` 可指定非空绝对路径。Linux 系统包启动器默认使用 `$XDG_DATA_HOME/icetudio` 或 `~/.local/share/icetudio`；更换启动路径不会自动迁移原数据。数据库格式不支持时明确拒绝并保留文件，不自动删除。

工程状态、编辑历史和撤销保存在 `icetudio.db`。时间图、音乐输入和 Native 对话草稿分别保存检查点；检查点不等于音乐已提交。存在未知提交时，先查询原操作的持久结果，不随当前选区改变目标，也不自动再次发送。删除对话不会删除音乐工程。

旧浏览器输入与附件可从“设置 → 外观”显式恢复；Linux profile 快照读取器已有实际验证，冲突不覆盖、原 profile 不删除。Windows/macOS 自动 profile 接管尚未验收。

默认 GM 音色为固定哈希的 MuseScore_General.sf3，已存在且校验符合要求时由便携打包脚本放在二进制旁；开发环境下载入口为 `scripts/fetch-soundfont.sh`，下载前遵守许可要求。[音色来源、许可与哈希](assets/soundfonts/NOTICE.md)随源码保留。可添加有权使用的本地 SF2/SF3；缺少默认音色时使用公式生成的 Basic Waves 兜底，它不模拟完整 GM 乐器。

## 验证与开发资料

已有依赖时，先构建卷帘资源，再运行常规检查：

```sh
go test ./...
go vet ./...
go test -race ./internal/desktop ./internal/app
npm --prefix frontend run test:music
npm --prefix frontend run test:ui
```

真实 Native/WebKit 回归要求可用桌面环境与系统运行库，只使用临时测试工程和本机模型夹具：

```sh
npm --prefix frontend run test:native
```

该回归不访问日常工程，也不验证外部供应商或物理输入设备。不要对日常数据使用独立验证入口的 `--smoke-edit`。原 React 行为断言归档在 [tests/retired-react](tests/retired-react/README.md)，当前测试通过不代表逐一复测了每个旧 DOM 断言。

- [迁移收尾与验收范围](docs/mygo-acceptance.md)：已执行检查、产物与未验收项。
- [迁移执行记录](docs/mygo-progress.md)：分批历史与问题修正；当前状态以收尾记录为准。
- [DAW 功能矩阵](docs/daw-expansion/feature-matrix.md)与[验收矩阵](docs/daw-expansion/acceptance.md)：后续目标，不是已交付功能清单。
- [开发约束](AGENTS.md)：工程、权限、数据保留与阶段顺序。

## 许可证

项目采用 [AGPL-3.0-only](LICENSE)。MyGo、GridSound 源码子集、FluidSynth 与音色保留各自许可和通知，见[第三方许可记录](licenses/README.md)。未核实授权的 GridSound assets、用户音色/采样和私人材料不随发行包采用。FL Studio 参考图片仅用于文档研究，不是产品资产；项目代码许可证不授予这些图片或输入音乐的再分发权。
