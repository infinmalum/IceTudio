#!/bin/sh
# 拉取 IceTUDIO 默认 GM 音色（MuseScore_General.sf3，MIT）到本地缓存与开发运行目录。
# 固定 URL + SHA-256；授权事实与打包义务见 assets/soundfonts/NOTICE.md。
set -eu

URL="https://ftp.osuosl.org/pub/musescore/soundfont/MuseScore_General/MuseScore_General.sf3"
SHA256="5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3"
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DEST="$ROOT/assets/soundfonts/MuseScore_General.sf3"

mkdir -p "$ROOT/assets/soundfonts"
if [ ! -f "$DEST" ] || [ "$(sha256sum "$DEST" | cut -d' ' -f1)" != "$SHA256" ]; then
  echo "下载 $URL"
  curl -fL --retry 3 -o "$DEST.part" "$URL"
  echo "$SHA256  $DEST.part" | sha256sum -c -
  mv "$DEST.part" "$DEST"
fi
echo "已就绪：$DEST"

# 开发环境：放到可执行文件旁，供运行时 bundled 候选路径直接命中。
if [ -d "$ROOT/build/bin" ]; then
  cp "$DEST" "$ROOT/build/bin/MuseScore_General.sf3"
  echo "已复制到 build/bin/（正式发行由打包任务打入安装物）"
fi
