#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
font="$root/assets/soundfonts/MuseScore_General.sf3"
if [ ! -f "$font" ]; then
  printf '%s\n' '缺少发行版默认音色；请先运行 scripts/fetch-soundfont.sh。' >&2
  exit 1
fi
printf '%s  %s\n' '5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3' "$font" | sha256sum -c -
