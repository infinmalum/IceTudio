# 默认 GM 音色：MuseScore_General.sf3（打包资产档）

本目录存放 IceTUDIO 发行物的默认 GM 音色及其授权档案。**SF2/SF3 大文件不进 Git 历史**
（见 `.gitignore`）；开发环境用 `scripts/fetch-soundfont.sh` 按固定哈希拉取，正式安装包
在构建/发行阶段把该文件打入发行物，保证**离线开箱即有默认可用音色**。

## 资产事实（2026-10-24 复核）

| 项 | 值 |
|---|---|
| 文件 | `MuseScore_General.sf3`（39,900,972 字节，压缩版；同目录 License 声明基于 MuseScore_General.sf2 v0.2，2020-05-13） |
| 来源 URL | https://ftp.osuosl.org/pub/musescore/soundfont/MuseScore_General/MuseScore_General.sf3 （MuseScore 官方 FTP 镜像；Last-Modified 2020-07-10） |
| SHA-256 | `5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3` |
| 许可 | **MIT**（原文件声明："MuseScore_General.sf2 is shared under the MIT license as described in COPYING, as was FluidR3Mono and FluidR3 before it."） |

## 署名链（任何衍生作品必须保留，原文件原话要求）

- FluidR3（原始采样）by Frank Wen, Copyright (c) 2000–02（README 内 2008 补充声明同为 MIT）
- 单声道化（FluidR3Mono）by Michael Cowgill, Copyright (c) 2014–17
- MuseScore_General 改编 by S. Christian Collins, Copyright (c) 2018–19
- Temple Blocks 采样 by Ethan Winer, Copyright (c) 2002
- Drumline Cymbals 采样 by Michael Schorsch, Copyright (c) 2016

原始许可与署名全文见随本目录一并提交的：

- `MuseScore_General_License.md`（License + FluidR3 README/COPYING 全文）
- `MuseScore_General_Changelog.md`
- `MuseScore_General_Sample_Sources.csv`（逐预置采样来源，打包前复核用）

## 不采用的候选

- `MS Basic.sf3`（MuseScore 4）：许可文件注明 "currently under discussion and may be
  subject to change before release"，不纳入。
- MuseScore master `share/sound/` 现只有 `FluidR3Mono_GM.sf3`（同 MIT、同一署名链），
  可作备选；本档固定为 `MuseScore_General.sf3`。

## 发行义务

- 发行物必须附带本目录的 License/NOTICE 文档，并列入第三方素材清单；
- 替换或升级默认音色时，必须重新复核实际文件的许可、来源与哈希，并更新本档；
- 未完成上述落档前，不得对外宣称"默认音色已就绪"（见 docs/p2-daw-workspace.md §3.2）。
