# P2 工作区与音色链契约（DAW 工作区、默认音色、乐器选择与试听）

状态：规格定稿（2026-10-23），来源为两轮评审结论与现状核查。实现以本文为准；改变产品范围需另行确认。布局以 FL Studio 的信息架构、工作区密度和常用 DAW 交互习惯为参考，目标是"熟悉 FL Studio 的人无需学习就能理解布局"，**不是**逐像素复制：不复制 Image-Line 商标、图标、图片、字体或其他专有视觉素材，图标使用自绘 SVG 或附带完整署名的开放图标库。

## 1. 布局结构

### 1.1 顶部固定 Transport / 工程工具栏

固定在窗口顶部，不随滚动移动。内容：

- 工程标题（可编辑，进入现有 project.rename 事务）；
- 播放 / 暂停 / 停止。暂停以现有播放/停止路径实现（记住停止位置并可从该位置继续），不引入第二套时钟；当前 Transport.tsx 只有"播放/停止/重新播放"，需补暂停语义；
- 播放位置（tick 与小节/拍双显示，沿用现有定位与进度条）；
- BPM（按当前位置的时间图实时显示）与拍号（同）；
- 吸附（snap）；
- 循环开关与范围；
- 节拍器：预留合理位置；功能未实现前禁用并解释可用下一步，不得做成假开关；
- 撤销 / 重做；
- 保存 / 导出；
- 音频状态（引擎、采样率、当前音源；当前 `audio-settings` details 中的详细设置移入 §4 的 SoundFont Manager 与 Browser 分区）。

### 1.2 左侧 Browser

- Projects（现有工程管理迁入）；
- Instruments / SoundFonts（§5 乐器选择 + §4 音色管理入口）；
- MIDI files（导入入口迁入或并存）；
- 最近使用；
- 搜索；
- Samples：暂未实现，不展示入口。

### 1.3 中央工作区

- 默认显示 Arrangement（多轨编排画布）；
- 双击片段或显式进入编辑时显示 Piano Roll；
- 允许：Arrangement 单独最大化、Piano Roll 单独最大化、Arrangement + Piano Roll 上下拆分；
- 面板尺寸持久化（在现有 shared/panes.tsx 宽度持久化基础上扩展为折叠/最大化/垂直分割状态）。

### 1.4 右侧 Agent 面板

保留 IceTudio 特有的 Agent Panel，不照搬 FL Studio。包含：conversations、prompt composer、当前工程/选区上下文。Agent changes、diff/preview、accept/reject/undo 属 P3 范围，本批次只预留结构位置，不实现、不显示空壳入口。

### 1.5 底部可折叠区域

为 Mixer / Track Inspector / 事件属性保留可折叠区域。首版实际内容为 Track Inspector 与事件属性（乐器、通道、颜色等高级参数）；Mixer 的音量/声像涉及工程模型新增非 MIDI 语义参数，实现前须按 §6 相同的契约修订路径另行决策，不在本批次自动扩大范围。

### 1.6 设置隔离

RuntimeConfig、MCP、Skills、开发者设置等不进入主要创作界面，保持在统一设置的独立入口（现状已是如此，重塑时不得退化）。上一批对话流 UX（发送即建对话、composer 工具条"新对话"）在重塑中必须保留语义。

## 2. 视觉语言

建立 IceTudio 自己的视觉语言，在现有 ice 主题变量体系上扩展：

- 深色作为默认 DAW 主题（当前浅色为默认，需翻转默认值；浅色保留为可选项）；
- 中性深灰背景；
- 冰蓝色作为选中/播放/Agent 状态强调色；
- clip 和 track 使用有限的用户可选颜色（调色板见 §6）；
- 减少大圆角卡片、网页式大留白、粗边框；
- 工具栏和编辑器紧凑，高信息密度；
- 时间轴占据最大面积。

## 3. 默认 GM 音色库（授权与打包）

### 3.1 候选与授权事实（2026-10-23 核查）

- 首选：MuseScore 3.5+ 发行资产 `MuseScore_General.sf3`。**MIT 授权**，署名链必须完整保留：Frank Wen（FluidR3 原始采样 © 2000–02, 2008）、Michael Cowgill（FluidR3Mono 单声道化 © 2014–17）、S. Christian Collins（MuseScore_General 改编 © 2018–19）、Ethan Winer（Temple Blocks © 2002）、Michael Schorsch（Drumline © 2016）；上游明确要求"The acknowledgements and copyright notices above must be included in any derivative work"。
- 备选：`FluidR3Mono_GM.sf3`（同样 MIT、同一署名链，为 MuseScore master 当前资产）。
- 不采用 `MS Basic.sf3`：为 MuseScore 4 alpha 衍生，其许可文件注明"currently under discussion and may be subject to change before release"。
- MuseScore master 的 `share/sound/` 已不含 MuseScore_General.sf3；下载源须固定到明确的 release/资产 URL 并记录哈希。

### 3.2 打包纪律

- **打包前必须对实际下载文件复核**：固定版本 URL、哈希、内嵌样本来源（上游 `MuseScore_General_Sample_Sources.csv`），不凭文件名假定授权；
- LICENSE 全文与 NOTICE（完整致谢链、来源 URL、复核日期）随发行物附带，进入第三方素材清单；
- 大文件不进 Git 历史：仓库内放 gitignored 本地缓存 + 固定版本的拉取脚本；正式安装包通过 release/构建资产打入；
- **发行物必须离线开箱即有默认可用音源**；不把完整音色做成必须联网下载的运行时依赖；
- 覆盖要求：GM program 0–127、打击组（percussion bank）、以及 Piano / Guitar / Bass / Strings / Brass / Reed / Pipe / Synth / Percussion / Effects 等家族可用（MuseScore_General 满足，验收时逐家族试听抽查）。

## 4. SoundFont Manager

统一音色管理，放置在 Browser 的 SoundFonts 分区，Transport 顶栏保留当前音源状态显示：

- 默认 SoundFont（打包音色）；
- 添加本地 .sf2 / .sf3（原生文件选择器，沿用现有 chooseFont 路径）；
- 移除用户 SoundFont；
- 切换默认音源；
- 展示名称、来源、路径、状态（加载成功/失败/校验中）；
- 加载失败给出明确错误与可用下一步（回退当前可用音源），不得静默失败；
- 用户音色优先级可调；
- **Basic Waves（内置公式波形）永远作为最后 fallback，不得删除**；
- 配置持久化沿用 `audio.json`，不参与音乐撤销（既有边界不变）；
- 播放中切换音源的行为遵循 [P2 播放契约](p2-playback.md)的状态恢复纪律，不得留下卡响音符。

## 5. 乐器选择与 GM 元数据

- 建立标准 GM instrument 元数据表：program number、display name、family、bank、percussion flag。内容为固定公开事实，随代码内置；
- 选择器按 16 个 GM 家族分组（Piano、Chromatic Percussion、Organ、Guitar、Bass、Strings、Ensemble、Brass、Reed、Pipe、Synth Lead、Synth Pad、Synth Effects、Ethnic、Percussive、Sound Effects），支持搜索；
- 轨道与 Inspector 显示 `Instrument: Acoustic Grand Piano`，不向普通用户直接暴露 program 数字；现有"乐器程序（1–128）"数字框退役到 Inspector 的高级参数（普通用户不首先看到）；
- 内部仍使用标准 bank/program，**不改变权威 project model 的 MIDI 语义**；
- 打击通道（channel 10，索引 9）按打击组显示。

## 6. Track Header 与轨道颜色

### 6.1 表头字段

每个 Track 至少直接可见：name、instrument、channel（小标签形式）、mute、solo、color。高级参数进 Inspector。

### 6.2 颜色模型（需契约修订的唯一模型触点）

颜色作为 Track/Clip 的 UI 元数据字段**进入权威 project model**：限定调色板枚举、不影响 MIDI 语义、SMF 导出不携带颜色、经统一事务提交并进入撤销/重做。理由：UI 本地存储会在换机/工程转移时丢失颜色，违反"GUI 与 Agent 共用同一工程模型"的产品原则。

实现前置：先补 [P1 工程模型契约](p1-project-model.md)的字段修订（调色板枚举值、默认色分配规则、SMF 导入忽略外部颜色、导出不写出），再实现；禁止绕过命令服务的 UI 层 hack。

## 7. 音色即时试听

新增绕过 timeline 的直接试听路径（不产生 Op、不进入工程修订/撤销历史）：

- Browser/选择器中点击乐器即试听：默认弹奏短 C4 或 C 大调和弦；
- 可停止试听；
- 切换音色立即使用新 SoundFont preset；
- audition 与工程播放不得互相留下 stuck notes：停止、切换、翻页、切工程、关窗时 all-notes-off 清理；
- **通道策略为实现前必须决策的开放点**：保留专用试听通道 vs 与工程播放互斥（64 轨已占满 4×16 通道）。决策记录进验证文档；
- 播放中切换默认音源时的在响音符处理遵循播放契约状态纪律；
- 钢琴卷帘允许点击左侧琴键直接试听当前轨道音色（同一 API）。

## 8. 演示闭环（首个可用里程碑验收）

完全离线可演示的闭环，优先级高于新增高级功能：

1. 新建工程 → 新建轨道 → 选择 Acoustic Grand Piano；
2. Piano Roll 添加音符 → 点击播放，立即听到真实钢琴类音色；
3. 改为 Violin，立即听到不同乐器；
4. 导入 MIDI，按 program/bank 使用合理 GM 音色；
5. Agent 修改指定片段（P3 范围）→ 显示修改差异 → A/B 或播放修改后结果 → undo；
6. 保存并重开 → 导出 MIDI。

第 5 步依赖 P3 落地；第 1–4、6 步在本批次完成后即应可离线演示。整体验收使用真实 Wails/WebKit 窗口与真实音频设备，headless 浏览器验证只作回归，不替代原生验收。

## 9. 测试与验收边界

- 自动化：SoundFont Manager 与试听的 Go 服务测试（列表/添加/移除/优先级/失败路径、试听不写工程、stuck notes 清理调用）；GM 元数据完整性测试（128 program、16 家族、打击标记）；布局与面板持久化的 product e2e 扩展；
- 原生验收：真实窗口下的布局持久化/最大化、离线开箱试听、演示闭环、播放中切音源；
- build pass 与浏览器 mock 不写成产品验收；授权落档（§3.2）完成前不得宣称"默认音色已就绪"。

## 10. 实施与验证记录（2026-10-24）

本批实现（代码接入与自动化验证；真实窗口/听感验收仍属 P2 整体原生验收）：

- §3 默认音色：`scripts/fetch-soundfont.sh` 固定 osuosl 镜像 URL + SHA-256（`5b85b6c2…b6fa3`，39,900,972 字节）；License/Changelog/Sample_Sources 原文与 NOTICE 落档 `assets/soundfonts/`（gitignore 大文件），并补入 `licenses/README.md`。运行时解析顺序：默认条目 → 打包音色（数据目录 `audio/`、可执行文件旁及 `soundfonts/` 子目录）→ 用户列表（优先级）→ Basic Waves；每步回退记入 Status notice，不静默。Linux `build:native` 任务复制音色到输出旁（缺失不阻断构建，发行验收要求存在）。
- §4 管理器：audio.json 升级 v2（`fonts[]`+`defaultFont`+`driver`）；v1 文件明确拒绝并保留，新增显式 `ResetSettings`（改名 `.unsupported` 保留后回默认）作为唯一恢复入口。显式"设为默认"先验证文件，失败保留在用引擎（`TestInvalidRefreshAndFontRetainWorkingState` 更新、`TestSoundFontManagerListAddRemovePriorityAndFallback`、`TestAudioSettingsRejectV1FileAndBundledResolution`）。
- §5 GM 元数据：`frontend/src/music/gm.ts` + `tests/gm.test.ts`（128/16/8 家族完整性、搜索、打击标记）。
- §6 颜色：P1 §11 修订先行；模型/操作/逆操作与测试（`TestTrackAndClipColorPaletteTransactions`）。
- §7 试听：**通道策略决策**——共用现有 4 个 synth 实例（不新增第 5 组，避免整份音色二次加载的内存），从当前播放快照未占用的全局通道自低向高取一个，与 Play/Seek/Configure 同把 control 锁串行；工程后续编辑占用该通道时由 plan 安装的清音顺带结束试听。实现中弃用"自高向低取空闲通道"：测试组合下对从未使用的最高通道（如 255）调用 FluidSynth 控制接口会段错误（本地可复现，单测单独运行不触发；未深究库内根因），自低向高取用经典低通道后套件稳定——此为已知规避，非根因修复。停止状态可发声（渲染循环为试听组补足非播放时段），时限到期或显式停止 all-notes-off；`TestAuditionBypassesTimelineAndCleansUp` 验证出声、修订不变、到期/停止静音与参数拒绝。
- §1/§2 布局：顶部 Transport（暂停/停止语义、小节拍+BPM+拍号+吸附只读显示、节拍器禁用占位、撤销/重做/导出/重命名）、左侧 Browser（工程/音色/最近/搜索，Samples 不展示）、编排/拆分/琴键视图与检查器折叠持久化、深色默认。product e2e 4 项通过（选择器更新为 Browser/Transport 位置，主题测试改为验证深色默认+真实切换）。
- CI：`.github/workflows/ci.yml`（见工程化基线条目）；`ICETUDIO_E2E_BROWSER` 覆盖浏览器二进制。

### 2026-10-24 用户视角走查与二次修正

首轮实现后按普通创作者旅程截图走查发现执行偏差：新结构叠加在旧载体上（工程标题卡/保存状态行/常驻时间图表单未收掉）、走带条溢出截断、左侧四栏挤压中心、乐器选择沉在页面底部、开发者术语（修订/PPQ/音频计数）直露。同日二次修正（减法优先）：收掉工程标题卡、保存状态行与"刷新工程"；走带条压为单行（标题改名入口、吸附控制、编排/拆分/琴键视图切换、保存与音源 chip、节拍器位），进度条细化为 6px；时间图收为单按钮（变化点计数入 tooltip）；导航栏取消并入浏览器（底部 助手/设置/主题；设置与助手页头部保留 设置/返回/主题）；工程列表显示更新时间而非修订/PPQ（修订只在保存 chip 与 tooltip）；轨道头乐器名可点击弹层选乐器并即时试听（原检查器选择器保留为高级入口）；SoundFont 管理表格改卡片行；无工程/无音轨空态提供居中主操作。选择器迁移同步 e2e（.project-strip→.transport-bar、.title=ID→data-id、project-header h1→transport-title），并恢复 ICETUDIO_MUSIC_BROWSER=1 真跑浏览器回归（此前被默认跳过而漏检，本轮起列入验证）。

未完成/边界：发行安装包级内嵌核验（P5）、真实窗口布局与听感验收、Mixer 音量/声像（未纳入）、节拍器功能、CI 在 GitHub 的实际首跑、检查器默认仍展开（兼顾表单自动化回归，折叠为默认待后续决策）。
