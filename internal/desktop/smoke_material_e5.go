package desktop

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"icetudio/internal/playback"
	"icetudio/internal/project"
)

func (a *Workspace) verifyMaterialsE5(dir string) error {
	filters, err := exec.Command("ffmpeg", "-hide_banner", "-filters").CombinedOutput()
	if err != nil || !strings.Contains(string(filters), "rubberband") {
		return nil
	}
	original := a.preview.Context()
	p, err := a.service.CreateProject("E5 素材验证", 480)
	if err != nil {
		return err
	}
	ctx := context.Background()
	fixture := filepath.Join(dir, "e5-source")
	if err = os.MkdirAll(fixture, 0700); err != nil {
		return err
	}
	wav := make([]byte, 44+48000*4)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 192000)
	binary.LittleEndian.PutUint16(wav[32:], 4)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 48000*4)
	for i := 0; i < 48000; i++ {
		sample := uint16(int16(12000 * math.Sin(2*math.Pi*430*float64(i)/48000)))
		binary.LittleEndian.PutUint16(wav[44+i*4:], sample)
		binary.LittleEndian.PutUint16(wav[46+i*4:], sample)
	}
	source := filepath.Join(fixture, "tone.wav")
	if err = os.WriteFile(source, wav, 0600); err != nil {
		return err
	}
	if _, err = a.service.ScanMaterials(ctx, fixture); err != nil {
		return err
	}
	items, err := a.service.Materials()
	if err != nil {
		return err
	}
	materialID := ""
	var selected int
	for i, item := range items {
		if item.Path == source {
			materialID, selected = item.ID, i
			break
		}
	}
	if materialID == "" {
		return fmt.Errorf("素材未索引")
	}
	if err = a.onMain(func() {
		a.install(p)
		a.materialShelf.open = true
		a.materialShelf.items = items
		a.chooseMaterial(items[selected])
		a.placeMaterial(EditTarget{p.ID, p.Revision}, materialID, "", 0, true, false)
	}); err != nil {
		return err
	}
	ready := func(pred func() bool) error {
		return waitCondition(func() bool {
			ok := false
			_ = a.onMain(func() { ok = pred() })
			return ok
		})
	}
	if err = ready(func() bool {
		return a.active.ID == p.ID && a.active.Revision == p.Revision+1 && len(a.active.State.Tracks[0].Audio) == 1
	}); err != nil {
		return fmt.Errorf("引用素材放置：%w", err)
	}
	if err = a.onMain(func() {
		track := a.active.State.Tracks[0]
		a.openPolish(track, track.Audio[0])
		if f := a.form; f != nil && f.kind == "audio_polish" {
			f.values.Polish.Percent, f.values.Polish.Correct, f.values.Polish.Target = "125", true, "69"
			a.preparePolish(f, false)
		}
	}); err != nil {
		return err
	}
	if err = ready(func() bool {
		return a.form != nil && !a.form.preparing && a.form.writes == 0 && a.form.values.Polish != nil && a.form.values.Polish.Asset != nil
	}); err != nil {
		return fmt.Errorf("精修候选：%w", err)
	}
	if err = a.onMain(func() { a.applyForm(a.form) }); err != nil {
		return err
	}
	if err = ready(func() bool {
		return a.form == nil && a.active.Revision == p.Revision+2 && a.active.State.Tracks[0].Audio[0].Frames == 60000
	}); err != nil {
		return fmt.Errorf("精修提交：%w", err)
	}
	if err = a.onMain(func() { a.placeMaterial(EditTarget{p.ID, p.Revision + 2}, materialID, "", 0, true, true) }); err != nil {
		return err
	}
	if err = ready(func() bool {
		return a.active.Revision == p.Revision+3 && len(a.active.State.Tracks) == 2 && a.active.State.Tracks[1].Sampler != nil
	}); err != nil {
		return fmt.Errorf("采样轨：%w", err)
	}
	current, err := a.service.GetProject(p.ID)
	if err != nil {
		return err
	}
	sampler := current.State.Tracks[1]
	tx, err := a.service.SubmitProjectOps(p.ID, "e5-notes", "gui", current.Revision, []project.Op{
		{Kind: "note.add", ClipID: sampler.Clips[0].ID, Notes: []project.NewNote{{Pitch: 60, Duration: 360, Velocity: 95}}},
		{Kind: "mixer.set", Mixer: &project.Mixer{Channels: []project.MixChannel{{ID: sampler.ID, Gain: .5, Pan: -1}}}},
	})
	if err != nil {
		return err
	}
	render := filepath.Join(dir, "e5-sampler.wav")
	if err = a.service.ExportProjectAudio(ctx, playback.ExportRequest{ProjectID: p.ID, Revision: tx.Revision, TrackID: sampler.ID, Bits: 16}, render); err != nil {
		return err
	}
	mix, err := os.ReadFile(render)
	if err != nil {
		return err
	}
	sound := false
	for i := 44; i < len(mix); i += 4 {
		sound = sound || binary.LittleEndian.Uint16(mix[i:]) != 0
		if binary.LittleEndian.Uint16(mix[i+2:]) != 0 {
			return fmt.Errorf("采样器未经过轨道声像")
		}
	}
	if !sound {
		return fmt.Errorf("采样器导出静音")
	}
	bundle := filepath.Join(dir, "e5-portable.icezip")
	if err = a.service.ExportAudioBundle(ctx, p.ID, tx.Revision, bundle); err != nil {
		return err
	}
	if err = os.Rename(source, source+".moved"); err != nil {
		return err
	}
	restored, err := a.service.ImportAudioBundle(ctx, bundle)
	if err != nil {
		return err
	}
	if restored.ID == p.ID || restored.State.Tracks[1].Sampler == nil || restored.State.AudioAssets[1].Derivation == nil {
		return fmt.Errorf("资源包缺少采样器或派生资产")
	}
	if err = a.service.DeleteProject(restored.ID); err != nil {
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
		a.materialShelf = materialShelf{}
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
