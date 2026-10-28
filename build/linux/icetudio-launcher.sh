#!/bin/sh
# System packages use an explicitly selected per-user data directory.
# Direct executable and AppImage launches retain their existing data location.
if [ "${ICETUDIO_DATA_DIR+x}" != x ]; then
  if [ -e /usr/local/bin/data ] || [ -L /usr/local/bin/data ]; then
    printf '%s\n' '检测到原系统安装的数据目录 /usr/local/bin/data。请显式设置 ICETUDIO_DATA_DIR 为要使用的绝对目录；原文件未修改。' >&2
    exit 1
  fi
  if [ -n "${XDG_DATA_HOME:-}" ]; then
    case "$XDG_DATA_HOME" in
      /*) ICETUDIO_DATA_DIR="$XDG_DATA_HOME/icetudio" ;;
      *) printf '%s\n' 'XDG_DATA_HOME 必须是绝对路径。' >&2; exit 1 ;;
    esac
  else
    case "${HOME:-}" in
      /*) ICETUDIO_DATA_DIR="$HOME/.local/share/icetudio" ;;
      *) printf '%s\n' 'HOME 必须是绝对路径，或显式设置 ICETUDIO_DATA_DIR。' >&2; exit 1 ;;
    esac
  fi
  export ICETUDIO_DATA_DIR
fi
exec /usr/lib/icetudio/icetudio "$@"
