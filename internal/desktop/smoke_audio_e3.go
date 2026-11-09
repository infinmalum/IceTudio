package desktop

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) verifyAudioE3(dir string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil
	}
	original := a.preview.Context()
	p, err := a.service.CreateProject("E3 音频验证", 480)
	if err != nil {
		return err
	}
	raw := make([]byte, 44+4800*4)
	copy(raw[:4], "RIFF")
	copy(raw[8:16], "WAVEfmt ")
	copy(raw[36:40], "data")
	binary.LittleEndian.PutUint32(raw[4:], uint32(len(raw)-8))
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 2)
	binary.LittleEndian.PutUint32(raw[24:], 48000)
	binary.LittleEndian.PutUint32(raw[28:], 192000)
	binary.LittleEndian.PutUint16(raw[32:], 4)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	binary.LittleEndian.PutUint32(raw[40:], uint32(len(raw)-44))
	for i := 44; i < len(raw); i += 4 {
		binary.LittleEndian.PutUint16(raw[i:], uint16(i%16000))
	}
	path := filepath.Join(dir, "e3-source.wav")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	asset, err := a.service.PrepareAudioAsset(context.Background(), path)
	if err != nil {
		return err
	}
	target := EditTarget{p.ID, p.Revision}
	clip := project.AudioClip{ID: "e3_audio", Name: "测试波形", AssetID: asset.ID, Frames: asset.Frames, Gain: 100}
	if err = a.onMain(func() {
		a.install(p)
		a.mutateAt(target, "edit", []project.Op{
			{Kind: "track.add", Track: &project.TrackSpec{ID: "e3_track", Name: "音频"}},
			{Kind: "audio.asset", AudioAsset: &asset},
			{Kind: "audio.add", TrackID: "e3_track", AudioClip: &clip},
		})
	}); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		ready := false
		_ = a.onMain(func() { ready = a.active.ID == p.ID && a.active.Revision == p.Revision+1 && !a.editBusy })
		return ready
	}); err != nil {
		return fmt.Errorf("E3 导入事务: %w", err)
	}
	if err = a.onMain(func() {
		a.openAudioClip(a.active.State.Tracks[0], a.active.State.Tracks[0].Audio[0])
		if a.form != nil {
			a.form.values.Audio.Reverse = true
			a.form.values.Audio.Frames = "2400"
			a.form.values.Audio.FadeIn = "12"
			a.changedForm(a.form)
		}
	}); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		ready := false
		_ = a.onMain(func() { ready = a.form != nil && a.form.writes == 0 && !a.form.saveFailed })
		return ready
	}); err != nil {
		return fmt.Errorf("E3 草稿: %w", err)
	}
	if err = a.onMain(func() { a.applyForm(a.form) }); err != nil {
		return err
	}
	if err = waitCondition(func() bool {
		ready := false
		_ = a.onMain(func() { ready = a.form == nil && a.active.Revision == p.Revision+2 })
		return ready
	}); err != nil {
		return fmt.Errorf("E3 音频编辑: %w", err)
	}
	ctx := context.Background()
	p, err = a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	if !p.State.Tracks[0].Audio[0].Reverse || p.State.Tracks[0].Audio[0].Frames != 2400 {
		return fmt.Errorf("E3 音频修改未持久化")
	}
	render := filepath.Join(dir, "e3-render.flac")
	if err = a.service.ExportProjectAudio(ctx, playback.ExportRequest{ProjectID: p.ID, Revision: p.Revision, Bits: 16}, render); err != nil {
		return err
	}
	reimport, err := a.service.PrepareAudioAsset(ctx, render)
	if err != nil || reimport.Frames != 2400 {
		return fmt.Errorf("E3 FLAC 往返: %d %v", reimport.Frames, err)
	}
	bundle := filepath.Join(dir, "e3-project.icezip")
	if err = a.service.ExportAudioBundle(ctx, p.ID, p.Revision, bundle); err != nil {
		return err
	}
	copy, err := a.service.ImportAudioBundle(ctx, bundle)
	if err != nil {
		return err
	}
	if copy.ID == p.ID || copy.State.AudioAssets[0].Digest != asset.Digest {
		return fmt.Errorf("E3 资源包副本不完整")
	}
	if err = a.service.DeleteProject(copy.ID); err != nil {
		return err
	}
	if err = a.service.DeleteProject(p.ID); err != nil {
		return err
	}
	old, err := a.service.GetProject(original.ProjectID)
	if err != nil {
		return err
	}
	return a.onMain(func() {
		a.install(old)
		for _, t := range old.State.Tracks {
			for _, c := range t.Clips {
				if c.ID == original.ClipID {
					a.selectClip(t, c)
					return
				}
			}
		}
	})
}
