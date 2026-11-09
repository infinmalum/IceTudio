# E1 工作区与连续编辑：交付记录

日期：2026-11-05。对象：基线 `67f6974` 上的本轮工作区改动。受众：维护者与后续编辑器实现者。执行范围以 [E1 执行契约](e1-workspace.md) 为准。

**状态：E1 代码与自动化闭环完成；A04/A15 的物理设备和人工验收仍待完成。** 本记录不是完整 DAW 或全部发布门槛通过声明。下一开发阶段是 E2 MIDI 创作；Agent 框架替换继续冻结。

## 已连接的能力

- Native 工作区：浏览器/助手显隐、编排/拆分/卷帘视图、布局重置、窄窗口侧栏抽屉，偏好独立持久化。隐藏卷帘不卸载它，关闭检查仍覆盖未完成手势与未知提交。
- Native Arrangement 与 WebView 卷帘改为连续导航，移除活动分页入口。支持精确远端定位、缩放、边缘滚动、长音符交叉视口显示；完整选择 ID 不随绘制窗口裁剪。底层卷帘使用 512 拍有界绘制窗口，手势之间重定位原点。
- 统一关闭/普通/三连音/自适应吸附与 Alt 临时关闭；位置支持小节:拍:拍内 tick 和整数 tick。变拍、小 PPQ、边界时间有精确往返检查。
- 播放头、定位、标尺循环区间及关闭/页跟随/连续跟随；手动导航暂停跟随，可显式恢复。仍调用既有 Go Playback 服务。
- 快捷键帮助、上下文操作、可配置工具键、真实音符复制粘贴与原子撤销；粘贴按目标 PPQ 换算并保留 MIDI 通道。输入框与合成 IME 事件不触发音乐快捷键。
- 密集完全重合音符按显示属性及选择状态合并 glyph，显示层数；全选、变换、复制、撤销及工程数据保留所有稳定 ID。隐藏的 vendor 力度滑块只在本应用 hosted 模式省略，Native 力度入口保留；未重写 GridSound 手势引擎。

服务、事务、修订冲突、草稿、CC/raw、候选 ghost、权限、Agent 内核与共享历史保持既有边界。本轮未新增 E2 pattern、CC 编辑器、音频、Mixer、插件或 ADK。

## 已执行验证

已有依赖下执行，Go 下载关闭（`GOPROXY=off GOTOOLCHAIN=local`），使用工具默认持久缓存：

```sh
npm --prefix frontend run test:music
npm --prefix frontend run build
go test ./...
go vet ./...
go test -race ./internal/desktop ./internal/app
ICETUDIO_MYGO_NATIVE=1 ICETUDIO_MYGO_EVIDENCE_DIR="$PWD/docs/daw-expansion/evidence/e1" go test ./internal/desktop -run '^(TestMyGoNative|TestActualWebKitProfile)' -count=1 -v
go build -o bin/icetudio .
```

音乐规则、全量 Go、vet、race、前端生产构建均通过。最终实际 MyGo/WebKit 套件通过（21.144 秒）：包括只读 profile、Native 工程/面板/工具、本地模型夹具审批、E1 长音/远端导航/复制粘贴/单次撤销、隐藏视图期间关闭取消、原操作 ID 恢复与既有工程保真回归。最终窗口尺寸修正后再次执行 desktop/app race，通过；音乐规则测试 18 项通过。

正式 `bin/icetudio` 已重新构建。额外使用临时数据启动：普通沙箱中创建数据库后提前退出，未保留退出日志，原因未确定；随后在真实桌面权限下复验，进程运行超过 3 秒且创建数据库，然后仅终止测试进程并清理临时数据。此短启动检查不替代上面的实际窗口交互回归。

本机为 Linux 7.3.1-1-cachyos x86_64，Ryzen 7 7735H（16 逻辑 CPU），GTK 3.24.52、WebKitGTK 2.54.2（4.1 API）、FluidSynth 2.6.2、MyGo 0.7.4、Go 1.27.3、Node 26.13.0。未隔离其他桌面负载，不是跨设备性能保证。

### 性能观测

每组 40 次合成滚动事件，计时到两次 requestAnimationFrame；不是物理输入延迟、帧耗时或音频延迟。提交到 Native 上下文就绪每组只有一次观测，不能称为 p95。

| 音符数/分布 | 滚动 p50 / p95（ms） | 已挂载 glyph | 提交到就绪（ms，单次） |
|---|---:|---:|---:|
| 1k 密集 | 51 / 66 | 80 | 149 |
| 1k 分散 | 42 / 82 | 8 | 129 |
| 10k 密集 | 56 / 69 | 80 | 392 |
| 10k 分散 | 49 / 86 | 8 | 365 |

四组全选/移调/撤销均覆盖全部 ID，最终音乐状态恢复。E1 小工程另测 40 次，p50 43ms、p95 82ms。原始数据：[dense.json](evidence/e1/dense.json)、[editor-e1.json](evidence/e1/editor-e1.json)、[result.json](evidence/e1/result.json)。这些观测符合 A04 的数值方向，但不替代其完整物理输入/拖动/IME 验收。

### 图像证据

[Native 工作区](evidence/e1/editor-e1-native.png)、[独立卷帘](evidence/e1/editor-e1-piano.png)、[1440 布局](evidence/e1/editor-e1-1440.png)、[1280 布局](evidence/e1/editor-e1-1280.png)、[200% 应用缩放](evidence/e1/editor-e1-200-percent.png)。文件名中的尺寸是测试请求的布局值，PNG 像素尺寸受窗口/显示缩放影响。

已查看最终独立卷帘与 1280 Native 图：未出现过时上下文错误；工作区与独立卷帘各自可见。MyGo Native CapturePage 不捕获嵌入 WebView 合成层，所以 Native 图的黑色卷帘区域不是完整桌面截图；卷帘另行捕获，不拼图冒充完整屏幕。200% 指应用缩放，不等于系统 200% DPI 验收。

## 尚待验收与复验条件

- 物理键鼠/触控板、中文输入法候选窗口与组合输入、系统 DPI、多显示器、屏幕阅读器和人工完整创作走查；A04/A15 不能标记全通过。
- 物理音频回环、监听延迟、长时负载、外部供应商模型、干净系统与跨平台安装继续沿用迁移及全 DAW 门槛，本轮不作通过声明。
- 连续导航、GridSound hosted 模式、修订/关闭协议、时间映射或布局阈值再变化时，重跑相应规则测试与实际窗口闭环；性能数字只适用于本记录环境和样本。

E2 开发须继续使用当前权威工程与事务服务，保留本节恢复/选择/性能覆盖；不能因 E1 自动化完成而取消尚未执行的验收项。
