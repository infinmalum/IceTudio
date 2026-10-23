# 音乐组件许可证核验

日期：2026-10-16。范围是本次选型的固定版本、实际实验依赖及拟采用的音频方案。属于工程合规初审，不是律师法律意见，不构成所有依赖、专利、商标、数据保护或发布渠道的完整法律审计。

## 已核验

| 对象 | 证据与许可 | 结论 |
|---|---|---|
| IceTudio 当前项目 | 根 LICENSE 为 AGPLv3 全文；README 和 frontend/package.json 明确 AGPL-3.0-only | 目前产品已经选择 AGPLv3，并非新增 GridSound 才成为 AGPL 项目 |
| GridSound UI c45632a4b6fbc38af87a6e68058bdb9d2c444242 | 固定提交根 LICENSE 为 AGPLv3 | 按 AGPLv3 合并发布可行；不能据此闭源发行衍生组合 |
| 必需 gs-utils 2db8ea908624f3c6e8ad24333249a3fdb8fbcf5a | 子模块自己的 LICENSE 也是 AGPLv3 | 不能仅替换钢琴卷帘而继续使用这个依赖，就认为消除了 AGPL 约束 |
| webaudio-pianoroll 523bc10e4b7e5d0a70b20caaeff5c14e4864c9de | Apache-2.0 全文 | 可用于现有路线；保留许可证/归属，标注修改，若分发对象存在 NOTICE 则处理其适用内容；本次未找到独立 NOTICE |
| gomidi v2.3.24 | 核心 LICENSE 为 MIT，作者 Marc Rene Arns | 可采用，分发保留版权与许可通知 |
| gomidi rtmididrv / 内嵌 RtMidi | 各自为 MIT 文本；RtMidi 有 Gary P. Scavone 版权通知 | 后续启用设备驱动时保留两份通知；请求回馈修改明确属于非强制条款；实验未链接设备驱动 |
| React/ReactDOM 18.2.0、scheduler、loose-envify、js-tokens | 核对实际安装包 LICENSE 与 package.json，均声明 MIT | 保留相应通知；版本与许可证 SHA256 见 results/licenses.json |

GridSound 的 AGPLv3 许可证模板附录含“or later”的示范声明，不能单靠模板示例把具体软件标为 or-later。本次只依赖明确存在的 v3 授权，不替上游确定更宽版本范围。

证据：[GridSound 固定许可](https://github.com/gridsound/gs-ui-components/blob/c45632a4b6fbc38af87a6e68058bdb9d2c444242/LICENSE)、[gs-utils 固定许可](https://github.com/gridsound/gs-utils/blob/2db8ea908624f3c6e8ad24333249a3fdb8fbcf5a/LICENSE)、[webaudio 固定许可](https://github.com/g200kg/webaudio-pianoroll/blob/523bc10e4b7e5d0a70b20caaeff5c14e4864c9de/LICENSE)、[gomidi 许可](https://gitlab.com/gomidi/midi/-/blob/v2.3.24/v2/LICENSE)。本机精确许可文件的指纹见 results/licenses.json。

## 对产品路线的影响

AGPL 允许商业使用和收费。沿 AGPLv3 发布组合软件时，应保留归属/许可、记录修改、按条款提供与二进制对应的源码及必要构建信息。公开一个无关版本或只提供改过的钢琴卷帘源码，不足以履行整个受覆盖组合的义务。最清晰的工程安排是在每个发布版本旁提供同版本源码包、构建说明和第三方通知。商业 EULA 不应剥夺 AGPL 已授予的权利。[AGPL §§1、4–6、10](https://www.gnu.org/licenses/agpl-3.0.html)

若修改版允许用户通过网络与其交互，§13 规定向这些用户显著提供对应源码获取方式。普通桌面软件调用外部模型 API，不能仅凭这一事实认定外部服务也受 AGPL 约束；反过来，仅将组成部分改成进程间通信，也不自动证明它们是独立作品。具体组合/部署边界需另审。[AGPL §13](https://www.gnu.org/licenses/agpl-3.0.html#section13)

若未来要闭源发行，不能仅修改本项目 LICENSE：自有代码能否另行授权取决于权利归属与贡献记录；GridSound、gs-utils 等第三方代码需要替换或取得额外授权。包一层 React、转译为 TypeScript、让 AI 改写原代码都不自动消除原作品许可义务。[AGPL §5](https://www.gnu.org/licenses/agpl-3.0.html#section5)

Apache-2.0 与 GPLv3 的组合兼容性有 ASF 官方说明；AGPLv3 与 GPLv3 的组合由其第13条处理。保留 Apache 自身许可和通知，不能把第三方文件的原许可直接抹掉。[ASF 兼容性说明](https://apache.org/licenses/GPL-compatibility.html)、[Apache §4、§6](https://www.apache.org/licenses/LICENSE-2.0)

## 素材：当前未放行

GridSound UI 的 assets 子模块固定指向 `3dc7233f711e1731e1ba2db750fb7b77ad1c7c6c`。通过 GitHub 完整递归树核对（truncated=false），未找到 LICENSE/COPYING/NOTICE/OFL 文件；唯一 README 内容是 `# assets`。不能据此断言每个素材都无授权，但没有足够证据整体打包使用。[固定素材仓库](https://github.com/gridsound/assets/tree/3dc7233f711e1731e1ba2db750fb7b77ad1c7c6c)

该仓库包含字体、图标、Logo、截图、采样索引和其他资源。UI 代码仓库的许可不能被当成对独立资源及第三方内容的完整权利证明。GitHub 可见或可 fork 也不等于允许任意再分发。[GitHub 许可说明](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository)

本次实验只下载 gs-utils，没有下载 assets。正式适配应使用自有或已明确授权的字体/图标，不复制上游 Logo 或对外暗示背书。逐项取得许可后才加入素材。截图仅是实验渲染证据，不是授予上游素材权利。

## 音频引擎与作品

FluidSynth 上游 LICENSE 为 LGPL-2.1 文本；尚未锁定发行版本和构建依赖，不能据此放行最终安装包。其官方 FAQ 建议使用可由用户替换的动态库，修改库本身时履行 LGPL 源码义务。动态链接也不免除许可/版权通知、库源码提供等要求；静态链接需要另核对重链接安排及 LGPL §6。[FluidSynth 许可](https://github.com/FluidSynth/fluidsynth/blob/master/LICENSE)、[官方 FAQ](https://github.com/FluidSynth/fluidsynth/blob/master/doc/wiki/LicensingFAQ.md)

Tone.js 上游代码许可为 MIT，但尚未选定和安装具体版本，其完整依赖与采样不在本次放行范围。[Tone.js 许可](https://github.com/Tonejs/Tone.js/blob/dev/LICENSE.md)

用户自行创作的 MIDI/音频不因为使用 AGPL 编辑器就自动成为 AGPL 作品；应分别检查输出是否包含受许可内容，以及输入音乐、采样和 SoundFont 的权利。FluidSynth 官方也明确音频输出与输入 MIDI/SoundFont 许可应分开看。[AGPL §2](https://www.gnu.org/licenses/agpl-3.0.html#section2)、[FluidSynth FAQ](https://github.com/FluidSynth/fluidsynth/blob/master/doc/wiki/LicensingFAQ.md)

目前没有选定内置 SoundFont/采样，因此尚无可随包分发或保证作品商用的音色清单。“免费下载”不等于允许再分发或商业作品使用。后续逐个核对音色再分发、作品使用、署名及样本提取限制。

## 本轮决定与未完成范围

1. 现有 AGPLv3 开源路线下，GridSound + gs-utils + gomidi 在所核验许可层面没有发现阻止组合的冲突，可以继续适配。
2. 不将 GridSound assets 或未审查音色加入发行包。
3. 发行时应随版本生成完整依赖与素材清单、许可正文、版权通知和对应源码；本报告与指纹清单不是完整发行用 THIRD_PARTY_NOTICES。
4. 原项目所有 Go/npm 传递依赖、系统库/AppImage 打包内容、未来插件 SDK、模型权重与服务条款尚未全面审计。
5. MCP/Skills 来源许可、反馈内容跨对话复用和分享的授权边界需在产品实现中明确。开放代码许可不自动授权上传用户作品、采样或私人对话。
6. 未做 IceTudio 商标检索、权利链调查、专利检索或特定司法辖区法律判断。最终闭源/双许可、云端运营与商业发行方案应交由熟悉开源合规的律师复核。
