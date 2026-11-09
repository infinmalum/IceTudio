package app

import (
	"context"
	"fmt"
	"math"
	"time"

	"icetudio/internal/audioasset"
	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (s *Service) TakeDevices(kind string) ([]audioasset.Device, error) {
	if kind == "midi" {
		return audioasset.MIDIDevices()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return audioasset.AudioDevices(ctx)
}

func (s *Service) StartTake(t audioasset.Take) error {
	return s.StartTakeLoop(t, 0)
}

func (s *Service) StartTakeLoop(t audioasset.Take, loopEnd int64) error {
	p, err := s.GetProject(t.ProjectID)
	if err != nil {
		return err
	}
	if p.Revision != t.Revision {
		return fmt.Errorf("工程修订已变化，请重新开始录音")
	}
	found := false
	for _, tr := range p.State.Tracks {
		if tr.ID == t.TrackID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("录音轨道不存在")
	}
	t.LoopFrames = 0
	if loopEnd != 0 {
		if t.Kind != "audio" || loopEnd <= t.Tick || loopEnd > project.MaxTick {
			return fmt.Errorf("音频循环区间无效")
		}
		timeline, err := playback.NewTimeline(&p.State)
		if err != nil {
			return err
		}
		t.LoopFrames = timeline.Frame(float64(loopEnd)) - timeline.Frame(float64(t.Tick))
		if t.LoopFrames < 1 || t.LoopFrames > project.MaxAudioFrames {
			return fmt.Errorf("循环音频帧数越界")
		}
	}
	if t.Kind == "midi" {
		return s.recordings.StartMIDI(t)
	}
	return s.recordings.Start(t)
}

func (s *Service) TakeStatus() audioasset.TakeStatus { return s.recordings.Status() }
func (s *Service) StopTake() {
	if s.recordings != nil {
		s.recordings.Stop()
	}
}
func (s *Service) PendingTakes(id string) ([]audioasset.TakeStatus, error) {
	takes, err := s.recordings.List()
	if err != nil {
		return nil, err
	}
	out := takes[:0]
	for _, take := range takes {
		if take.ProjectID != id {
			continue
		}
		_, found, e := s.GetProjectTransaction(id, take.ID)
		if e != nil {
			return nil, e
		}
		if !found {
			out = append(out, take)
		}
	}
	return out, nil
}

// SaveTake commits exactly once in its original project. Choosing a newer
// revision/position is a separate explicit recovery action by the user.
func (s *Service) SaveTake(id, takeID string, rev, tick int64, acceptCurrent bool) (project.Result, error) {
	if old, found, err := s.GetProjectTransaction(id, takeID); err != nil || found {
		return old, err
	}
	p, err := s.GetProject(id)
	if err != nil {
		return project.Result{}, err
	}
	if p.Revision != rev {
		return project.Result{}, fmt.Errorf("工程已变化，请刷新再恢复")
	}
	pending, err := s.PendingTakes(id)
	if err != nil {
		return project.Result{}, err
	}
	var take audioasset.TakeStatus
	for _, t := range pending {
		if t.ID == takeID {
			take = t
			break
		}
	}
	if take.ID == "" || take.Active {
		return project.Result{}, fmt.Errorf("原 take 不存在或仍在录制")
	}
	if !acceptCurrent && (take.Revision != rev || take.Tick != tick) {
		return project.Result{}, fmt.Errorf("原修订或位置有变化，请明确选择按当前修订恢复")
	}
	if tick < 0 || tick > project.MaxTick {
		return project.Result{}, fmt.Errorf("录音放置位置无效")
	}
	var ops []project.Op
	if take.Kind == "audio" {
		_, asset, err := s.recordings.Recover(takeID)
		if err != nil {
			return project.Result{}, err
		}
		ops = []project.Op{{Kind: "audio.asset", AudioAsset: &asset}, {Kind: "audio.add", TrackID: take.TrackID,
			AudioClip: &project.AudioClip{ID: project.NewID("ac"), Name: asset.Name, AssetID: asset.ID, Start: tick, Frames: asset.Frames, Gain: 100}}}
	} else {
		_, frames, err := s.recordings.RecoverMIDI(takeID)
		if err != nil {
			return project.Result{}, err
		}
		ops, err = midiTakeOps(&p.State, take.TrackID, tick, frames)
		if err != nil {
			return project.Result{}, err
		}
	}
	return s.SubmitProjectOps(id, takeID, "gui", rev, ops)
}

func midiTakeOps(st *project.State, track string, start int64, frames []audioasset.MIDIFrame) ([]project.Op, error) {
	timeline, err := playback.NewTimeline(st)
	if err != nil {
		return nil, err
	}
	base := timeline.Frame(float64(start))
	id := project.NewID("c")
	spec := &project.ClipSpec{ID: id, TrackID: track, Name: "MIDI 录音", Start: start, Length: 1}
	notes := []project.NewNote{}
	controls := []project.EventSpec{}
	var held [16][128][]project.NewNote
	last := int64(0)
	for _, f := range frames {
		if f.Nanos < 0 || f.Nanos > int64(10*time.Minute) {
			return nil, fmt.Errorf("MIDI 时间戳越界")
		}
		data := f.Data
		if len(data) < 2 || len(data) > 3 || data[0] < 0x80 || data[0] >= 0xf0 || data[1] > 127 || len(data) == 3 && data[2] > 127 {
			continue
		}
		tick := max(last, int64(math.Round(timeline.Tick(base+f.Nanos*playback.SampleRate/int64(time.Second))))-start)
		if tick >= project.MaxTick {
			return nil, fmt.Errorf("MIDI 录音超出时间上限")
		}
		last = tick
		ch, a := int(data[0]&15), int(data[1])
		kind := data[0] & 0xf0
		if kind != 0xc0 && kind != 0xd0 && len(data) != 3 {
			continue
		}
		b := 0
		if len(data) == 3 {
			b = int(data[2])
		}
		event := project.EventSpec{Tick: tick, Channel: ch}
		switch kind {
		case 0x80, 0x90:
			if kind == 0x90 && b > 0 {
				held[ch][a] = append(held[ch][a], project.NewNote{Start: tick, Pitch: a, Velocity: b, Channel: ch})
			} else if q := held[ch][a]; len(q) > 0 {
				n := q[0]
				n.Duration = max(1, tick-n.Start)
				notes = append(notes, n)
				held[ch][a] = q[1:]
			}
			continue
		case 0xa0:
			event.Kind, event.Note, event.Value = project.KindPolyAftertouch, a, b
		case 0xb0:
			event.Kind, event.Controller, event.Value = project.KindCC, a, b
		case 0xc0:
			event.Kind, event.Program = project.KindProgram, a
		case 0xd0:
			event.Kind, event.Value = project.KindChannelAftertouch, a
		case 0xe0:
			event.Kind, event.Value = project.KindPitchBend, a|b<<7
		default:
			continue
		}
		controls = append(controls, event)
	}
	for _, ch := range held {
		for _, key := range ch {
			for _, n := range key {
				n.Duration = max(1, last-n.Start)
				notes = append(notes, n)
			}
		}
	}
	if len(notes)+len(controls) == 0 {
		return nil, fmt.Errorf("没有可恢复的 MIDI 通道事件")
	}
	spec.Length = last + 1
	ops := []project.Op{{Kind: "clip.add", Clip: spec}}
	if len(notes) > 0 {
		ops = append(ops, project.Op{Kind: "note.add", ClipID: id, Notes: notes})
	}
	if len(controls) > 0 {
		ops = append(ops, project.Op{Kind: "control.add", ClipID: id, Events: controls})
	}
	return ops, nil
}
