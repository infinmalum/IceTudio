# 本批原生音频许可记录

范围：2026-10-17 P2 播放实现。不是整个应用或最终安装包的第三方许可清单。

- FluidSynth：开发/运行验证 2.6.1，本机包声明 LGPL-2.1-or-later；公开头文件亦声明 LGPL 2.1 或后续版本。应用动态链接 `libfluidsynth.so.3`，未修改库代码。[许可正文](FluidSynth-LGPL-2.1.txt)取自 [v2.6.1 LICENSE](https://github.com/FluidSynth/fluidsynth/blob/v2.6.1/LICENSE)。对应上游源码为同版本标签；实际再分发的系统补丁、依赖、替换/重链接与源码提供安排仍须按发行内容核查。源码地址不替代发行者应履行的源码义务。
- IceTudio Basic Waves：`internal/playback/basicfont.go` 从数学公式生成 SoundFont；没有复制第三方采样、录音或 GridSound assets。生成的波形与预置数据以 [CC0-1.0](CC0-1.0.txt)提供；生成器代码仍沿项目 AGPL-3.0-only。CC0 正文镜像来自 [SPDX license-list-data](https://github.com/spdx/license-list-data/blob/main/text/CC0-1.0.txt)，权威文本见 [Creative Commons](https://creativecommons.org/publicdomain/zero/1.0/legalcode.en)。
- MuseScore_General.sf3（默认 GM 音色，2026-10-24 纳入）：MIT；署名链 Frank Wen（FluidR3 © 2000–02）、Michael Cowgill（FluidR3Mono © 2014–17）、S. Christian Collins（MuseScore_General © 2018–19）、Ethan Winer（Temple Blocks © 2002）、Michael Schorsch（Drumline © 2016），上游要求署名随衍生作品保留。固定 URL 与 SHA-256、原始许可全文与逐预置采样来源存于 [assets/soundfonts/NOTICE.md](../assets/soundfonts/NOTICE.md)；大文件不进 Git（scripts/fetch-soundfont.sh 拉取校验）。
- 用户自己选择的音色不复制进工程或发行包，本项目不替这些文件授予许可。内置基础波形不是 General MIDI 拟真音色库。

旧音乐组件核验见 [实验许可初审](../experiments/music-spike/LICENSE-REVIEW.md)；GridSound 源码子集的通知在 `frontend/public/vendor/gridsound/NOTICE.md`。安装包包含的传递系统库、素材和完整应用依赖仍由 P5 单独验收。
