#!/usr/bin/env bash
# Build using already-installed linuxdeploy/appimage plugin tools; never fetch
# executables implicitly. Linux portable tar packaging needs no extra tools.
set -euo pipefail
: "${APP_NAME:?}" "${APP_BINARY:?}" "${ICON_PATH:?}" "${DESKTOP_FILE:?}"
command -v linuxdeploy >/dev/null || { echo 'Install linuxdeploy and its AppImage output plugin before requesting AppImage packaging.' >&2; exit 1; }
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
appdir="$stage/$APP_NAME.AppDir"
mkdir -p "$appdir/usr/bin" "$appdir/usr/share/licenses/$APP_NAME"
cp "$APP_BINARY" "$appdir/usr/bin/$APP_NAME"
cp LICENSE "$appdir/usr/share/licenses/$APP_NAME/LICENSE"
cp -r licenses "$appdir/usr/share/licenses/$APP_NAME/third-party"
cp frontend/public/vendor/gridsound/LICENSE "$appdir/usr/share/licenses/$APP_NAME/GridSound-LICENSE"
cp frontend/public/vendor/gridsound/gs-utils/LICENSE "$appdir/usr/share/licenses/$APP_NAME/gs-utils-LICENSE"
cp frontend/public/vendor/gridsound/NOTICE.md "$appdir/usr/share/licenses/$APP_NAME/GridSound-NOTICE.md"
cp -r assets/soundfonts "$appdir/usr/share/licenses/$APP_NAME/soundfont-notices"
# Package only the fixed, licensed font when available.
find "$appdir/usr/share/licenses/$APP_NAME/soundfont-notices" -type f \( -name "*.sf2" -o -name "*.sf3" \) -delete
cp "$ICON_PATH" "$appdir/$APP_NAME.png"
cp "$DESKTOP_FILE" "$appdir/$APP_NAME.desktop"
if [ -f assets/soundfonts/MuseScore_General.sf3 ]; then
  printf '%s  %s\n' '5b85b6c2c61d10b2b91cddd41efcce7b25cd31c8271d511c73afafbef20b6fa3' assets/soundfonts/MuseScore_General.sf3 | sha256sum -c -
  cp assets/soundfonts/MuseScore_General.sf3 "$appdir/usr/bin/"
fi
linuxdeploy --appdir "$appdir" --output appimage
