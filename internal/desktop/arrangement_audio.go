package desktop

import (
	"math"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

const waveHeight float32 = 38

type waveClip struct {
	offsetStart, offsetEnd int64
	clip                   project.AudioClip
	asset                  project.AudioAsset
	left, width            float32
	lane                   int
}

func waveClips(track project.Track, assets map[string]project.AudioAsset, timeline playback.Timeline, start, end int64, zoom float64) ([]waveClip, int) {
	ends := []int64{}
	out := []waveClip{}
	for _, c := range track.Audio {
		to := int64(math.Ceil(timeline.Tick(timeline.Frame(float64(c.Start)) + c.Frames)))
		if to <= start || c.Start >= end {
			continue
		}
		lane := 0
		for lane < len(ends) && ends[lane] > c.Start {
			lane++
		}
		if lane == len(ends) {
			ends = append(ends, to)
		} else {
			ends[lane] = to
		}
		left := float32(float64(max(start, c.Start)-start) / float64(timeline.PPQ) * zoom)
		width := float32(float64(min(end, to)-max(start, c.Start)) / float64(timeline.PPQ) * zoom)
		frame := timeline.Frame(float64(c.Start))
		first := max(int64(0), timeline.Frame(float64(start))-frame)
		last := min(c.Frames, timeline.Frame(float64(end))-frame)
		out = append(out, waveClip{first, last, c, assets[c.AssetID], left, max(6, width), lane})
	}
	return out, len(ends)
}

func (v *arrangementView) drawWave(c *ui.Context, p storage.Project, t project.Track, w waveClip, top float32, actions arrangementActions) {
	label := w.clip.Name
	if label == "" {
		label = "音频片段"
	}
	b := ui.Button(c.Key(w.clip.ID), label).Absolute().Left(w.left).Top(top + float32(w.lane)*waveHeight).
		Width(w.width).Height(waveHeight - 3).Background(ui.Hex("#31505b")).Label(label)
	b.Tooltip("打开音频片段，调整源窗口、淡变、增益、反向或拆分")
	b.Draw(func(paint *ui.Painter, r ui.Rect) {
		peaks := w.asset.Peaks
		if len(peaks) == 0 || w.asset.Frames <= 0 {
			return
		}
		bins := min(len(peaks), max(1, int(r.W/2)))
		first, span := float64(w.offsetStart), float64(w.offsetEnd-w.offsetStart)
		for i := 0; i < bins; i++ {
			f := first + span*float64(i)/float64(bins)
			if w.clip.Reverse {
				f = float64(w.clip.Frames-1) - f
			}
			portion := (float64(w.clip.Source) + f) / float64(w.asset.Frames)
			p := min(len(peaks)-1, max(0, int(portion*float64(len(peaks)))))
			h := float32(peaks[p]) / 32767 * (r.H/2 - 3)
			x := r.X + float32(i)*r.W/float32(bins)
			paint.Line(x, r.Y+r.H/2-h, x, r.Y+r.H/2+h, 1, ui.Hex("#5aa7b4"))
		}
	})
	if v.handles == nil {
		v.handles = map[string]*ui.Handle{}
	}
	ref := v.handles[w.clip.ID]
	if ref == nil {
		ref = &ui.Handle{}
		v.handles[w.clip.ID] = ref
	}
	b.Bind(ref)
	b.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 || ev.Mods&ui.Shift != 0 || v.gesture != nil || v.audioDrag != nil || actions.Writable == nil || !actions.Writable() {
				return false
			}
			v.audioDrag = &audioClipDrag{target: EditTarget{p.ID, p.Revision}, clip: w.clip, track: t.ID, x: ev.X, start: w.clip.Start}
			actions.Freeze(true)
			return true
		case ui.InputKeyDown:
			if ev.Key == ui.KeyEscape && v.audioDrag != nil {
				v.cancel(actions)
				return true
			}
		case ui.InputPointerMove, ui.InputPointerUp:
			d := v.audioDrag
			if d == nil || d.clip.ID != w.clip.ID {
				return false
			}
			if ev.Canceled {
				v.cancel(actions)
				return true
			}
			step := editorSnap(v.snap, v.zoom, p.PPQ) * float64(p.PPQ)
			if ev.Mods&ui.Alt != 0 {
				step = 1
			}
			dx := float64(ev.X - d.x)
			d.moved = d.moved || math.Abs(dx) > 3
			d.start = max(0, min(project.MaxTick, d.clip.Start+int64(math.Round(math.Round(dx/v.zoom*float64(p.PPQ)/step)*step))))
			if ev.Kind == ui.InputPointerUp {
				v.cancel(actions)
				if !ref.Focused(c) {
					return true
				}
				if !d.moved && actions.AudioOpen != nil {
					actions.AudioOpen(t, w.clip)
				} else if d.start != d.clip.Start {
					d.clip.Start = d.start
					actions.Commit(d.target, []project.Op{{Kind: "audio.update", TrackID: d.track, AudioClip: &d.clip}})
				}
			}
			return true
		}
		return false
	})
	if d := v.audioDrag; d != nil && d.clip.ID == w.clip.ID && d.moved {
		x := w.left + float32(float64(d.start-d.clip.Start)/float64(p.PPQ)*v.zoom)
		ui.Box(c.Key(w.clip.ID+"-drag")).Absolute().Left(x).Top(top+float32(w.lane)*waveHeight).
			Width(w.width).Height(waveHeight-3).Border(2, c.Theme().Accent)
	}
	b.OnClick(func() {
		if actions.AudioOpen != nil {
			actions.AudioOpen(t, w.clip)
		}
	})
}

type audioClipDrag struct {
	target EditTarget
	clip   project.AudioClip
	track  string
	x      float32
	start  int64
	moved  bool
}
