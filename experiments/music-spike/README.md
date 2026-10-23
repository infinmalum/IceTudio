# IceTudio 音乐库验证

日期：2026-10-16。独立选型实验，不接入产品，不修改主项目依赖。

## 范围与版本

- GridSound UI：`c45632a4b6fbc38af87a6e68058bdb9d2c444242`，其 gs-utils 使用子模块固定提交 `2db8ea908624f3c6e8ad24333249a3fdb8fbcf5a`。
- webaudio-pianoroll：`523bc10e4b7e5d0a70b20caaeff5c14e4864c9de`。
- gomidi：`gitlab.com/gomidi/midi/v2 v2.3.24`。
- React / ReactDOM：18.2.0；本机 Node 26.11.1、Go 1.27.1、Linux Chromium headless。

上游地址：[GridSound](https://github.com/gridsound/gs-ui-components)、[webaudio-pianoroll](https://github.com/g200kg/webaudio-pianoroll)、[gomidi](https://gitlab.com/gomidi/midi)。前两者分别声明 AGPL-3.0、Apache-2.0，gomidi 声明 MIT。本实验不复制第三方代码进产品。

## 复现

在仓库根目录依次运行（需要 git、npm、支持全局 WebSocket 的现代 Node、Go、chromium；端口 9398 应空闲）：

```sh
node experiments/music-spike/prepare.mjs
node experiments/music-spike/browser.mjs
go -C experiments/music-spike test -v ./...
```

依赖源码和浏览器用户目录位于 `.cache/music-spike/`；Go/npm 使用用户级缓存。脚本结束关闭浏览器与本地 HTTP 服务。

浏览器验证应通过。**Go 接受性测试当前有两个预期可复现的失败，故命令返回非零：SMPTE panic 和截断头被接受。没有把缺陷改成“测试通过”。** 仅复验正常 PPQ 往返可运行：

```sh
go -C experiments/music-spike test -v -run 'TestEventRoundTrip/format[01]_ppq' .
```

结果保存在 `.cache/music-spike/browser-results.json` 和截图中；本次结果快照保存在 `results/`。失败来自固定的上游版本，实验没有修补依赖。

## GridSound 结果

浏览器加载所需组件集合，通过 React effect 创建/销毁实例，用浏览器输入协议执行实际鼠标操作，而不是手工调用编辑回调。

- 拖动音符产生 `move` 命令，拉伸产生 `cropEnd` 命令，带稳定音符 ID。
- 宿主接收命令并更新数据，通过 `$changeKeyProp` 更新显示；宿主撤销恢复位置/音高/时长。
- 代码模拟 Agent 的外部编辑可以更新同一对象并撤销。没有调用模型。
- 宿主示例修订检查拒绝过时修改；这是实验宿主实现的能力，**不是 GridSound 自带并发控制**。
- React 普通卸载/重新挂载后只有一个编辑器，运行无 JS exception。未验证 StrictMode 双 effect、拖动中卸载和长期内存泄漏。
- 组件渲染是 DOM，每个音符连同关联 UI 增加约 11 个节点。本次 1,000 音符创建约 132ms、逐个位置更新约 11ms；10,000 音符创建约 1,127ms、更新约 104ms，约 110,240 个后代节点。

时间是本机单次同步调用耗时，不包含完整帧呈现，不是 FPS 或跨平台性能承诺。不能据此认定十万音符或播放时持续编辑可用。大工程应限定显示片段/视口，并单独验证虚拟化或 Canvas 渲染。

接入成本：此版本没有 npm package.json，通过全局 `$` / GSU 工具、基类、模板、CSS 和多个关联组件运行。实验显式加载 33 个 JS 文件（含模板，不代表最小依赖集合）。内置 gain/pan/filter/LFO 参数具有自身合成器语义；MIDI velocity/CC 需要单独映射，不能直接把 gain 当作完整力度/控制器编辑器。全局资源需一次加载，React 仅管理实例生命周期。

结论：**可作为首版钢琴卷帘的受控适配起点，不宜原封不动成为工程状态所有者。** 工程事务、撤销、授权、Agent 冲突检测属于 IceTudio。长编辑手势期间发生外部修改的协调、批量原子操作、键盘/多选/复制删除、变拍网格、差异预览仍需后续验证。

## webaudio-pianoroll 结果

- 真鼠标选择后再拖动，内部 sequence 就地改变；外部赋值 sequence 并 redraw 可恢复快照。
- 这次拖动未触发 change/input；源码中 sendEvent 有定义但没有编辑路径调用。
- 输入音符额外的 id/velocity 字段在被拖动时仍存在，但创建音符只提供 t/n/g/f，编辑中还会加入 on/ot/og 等临时字段。
- 需要自行改造命令通知、ID 生成、权威状态映射和事务边界。快照恢复不能当作已实现统一撤销。
- 固定提交最后提交日期为 2022-12-20；GridSound 固定提交日期为 2026-10-16。日期只描述此次检查的分支，不证明未来维护承诺。

结论：**不选作当前产品主编辑器依赖。** 更适合作为简单交互参考，Canvas 本身的优势不能抵消其状态接口适配成本。

## gomidi 结果

输入 MIDI 由测试独立编码，包含 running status，不用被测库生成输入。解析后检查具体事件，再比较两种输出编码模式的逐轨绝对 tick、事件顺序和消息字节。

通过：SMF 0/1、480 PPQ、多轨、同音高重叠 note-on、带释放力度的 note-off、零力度 note-on、CC64、program change、pitch bend、channel/poly pressure、SysEx、tempo/拍号变更、调号、marker 和 sequencer metadata；960 tick 跨速度变化得到 1.1 秒。

这证明测试覆盖的原始事件往返，不证明音符配对策略、任意未知事件、所有 MIDI 变体、真实设备或音频正确性。工程导入层仍需保存无法映射到音符的事件，不能仅保存 note 数组。

失败：

1. SMPTE -25fps / 40 subframes 的 SMF 读取 panic。`smf/smf.go` 中 `calculateAbsTimes` 将 TimeFormat 强转为 MetricTicks；本次输入得到 TimeCode。需在导入前识别并明确拒绝未支持的时间格式，或修复/替换这一能力后再开放。
2. 只有 13 字节、连 14 字节标准头都不完整的输入没有返回错误。0/4 字节和轨尾截断样例返回错误。需独立校验头、轨块长度、资源上限和结束状态，不能只依赖 ReadFrom 返回值。

结论：**有条件采用，首版明确 PPQ 范围并建设导入校验，不能声称支持所有 SMF。** 当前版本不能无保护读取不可信文件；未在此次实验里修复库或实现生产导入器。

## 未覆盖

Wails 原生 WebView、真实 Go/前端命令往返、实际音频输出、FluidSynth/Tone.js 播放比较、MIDI 硬件、真实模型、项目持久化/恢复，以及跨平台性能均未验证。本轮结论仅用于钢琴卷帘与 MIDI 数据层选型，不代表整套音乐软件已经跑通。
