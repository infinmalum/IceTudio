//go:build !linux || (!amd64 && !arm64)

package desktop

import "github.com/egoist/mygo/ui"

func (a *Workspace) syncPianoViewport(*ui.Painter, ui.Rect) {}
