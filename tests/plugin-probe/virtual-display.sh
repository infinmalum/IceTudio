#!/bin/sh
# Run inside the worker namespace, without the user's X or Wayland sockets.
set -eu
mkdir -m 700 "$XDG_RUNTIME_DIR"
mkdir -m 1777 /tmp/.X11-unix
exec dbus-run-session -- kwin_wayland --virtual --xwayland --socket "$WAYLAND_DISPLAY" \
    --no-lockscreen --no-global-shortcuts --no-kactivities --exit-with-session /editor-session
