package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"icetudio/internal/playback"
	"icetudio/internal/pluginhost"
	"icetudio/internal/project"
)

// PluginCatalog is local desktop inventory. No IPC or Agent edit can
// authorize plugin execution through these methods.
func (s *Service) PluginCatalog() ([]pluginhost.Module, error) {
	return s.plugins.List()
}

func (s *Service) ScanPluginFolder(ctx context.Context, folder string, progress func(int, int)) error {
	return s.plugins.Scan(ctx, folder, progress)
}

func (s *Service) RetryPluginScan(ctx context.Context, path string) error {
	return s.plugins.Retry(ctx, path)
}

func (s *Service) SetPluginBlocked(path string, blocked bool) error {
	if err := s.plugins.SetBlocked(path, blocked); err != nil {
		return err
	}
	if blocked && s.playback != nil {
		s.playback.Stop()
	}
	return nil
}

// PreparePluginSlot loads an explicitly selected catalog entry in isolation.
// The returned state is a draft. Only the normal project transaction can
// persist it, and that transaction still checks the captured revision.
func (s *Service) PreparePluginSlot(ctx context.Context, identity pluginhost.Identity, kind, name string, previous *project.PluginState) (*project.PluginState, error) {
	var blob []byte
	if previous != nil {
		if identity.Format != previous.Format || identity.Class != previous.Class {
			return nil, fmt.Errorf("重新定位需要同格式和类 ID")
		}
		blob = previous.State
	}
	instance, err := s.plugins.Open(ctx, identity, kind, blob, false)
	if err != nil {
		return nil, err
	}
	defer instance.Close()
	p := &project.PluginState{
		Format: identity.Format, Class: identity.Class, SHA256: identity.SHA256, Name: name, Version: instance.Version, Vendor: instance.Vendor, State: instance.State,
	}
	for _, param := range instance.Parameters {
		value := param.Value
		if previous != nil {
			for _, old := range previous.Parameters {
				if param.ID == old.ID {
					value = old.Value
					break
				}
			}
		}
		p.Parameters = append(p.Parameters, project.PluginParameter{ID: param.ID, Name: param.Name, Default: param.Value, Value: value})
	}
	if previous != nil {
		for _, old := range previous.Parameters {
			if !slices.ContainsFunc(p.Parameters, func(v project.PluginParameter) bool { return v.ID == old.ID }) {
				return nil, fmt.Errorf("插件版本缺少参数 %s；已保留原状态与自动化", old.Name)
			}
		}
	}
	return p, nil
}

// PrepareTrackFreeze renders a frozen copy into managed audio storage. The
// caller publishes its asset and mixer marker in the same revision-checked op.
func (s *Service) PrepareTrackFreeze(ctx context.Context, id string, revision int64, track string) (project.AudioAsset, error) {
	if s.playback == nil {
		return project.AudioAsset{}, fmt.Errorf("此宿主未启用原生渲染")
	}
	dir, err := os.MkdirTemp("", "icetudio-freeze-")
	if err != nil {
		return project.AudioAsset{}, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "冻结轨道.wav")
	if err := s.playback.ExportTrackFreeze(ctx, id, revision, track, path); err != nil {
		return project.AudioAsset{}, err
	}
	return s.PrepareAudioAsset(ctx, path)
}

func (s *Service) PreviewSynth(ctx context.Context, patch project.SynthPatch) error {
	return s.library.PreviewPCM(ctx, .7, func(ctx context.Context) ([]byte, error) {
		return playback.SynthPreview(ctx, patch)
	})
}

// EditPluginSlot runs the vendor editor on a private display and returns a
// checkpoint only after the desktop explicitly asks to capture it.
func (s *Service) EditPluginSlot(ctx context.Context, previous project.PluginState, kind string, capture <-chan struct{}, ready func()) (*project.PluginState, error) {
	identity := pluginhost.Identity{Format: previous.Format, Class: previous.Class, SHA256: previous.SHA256}
	var overrides []pluginhost.Parameter
	for _, p := range previous.Parameters {
		if p.Value != p.Default {
			overrides = append(overrides, pluginhost.Parameter{ID: p.ID, Value: p.Value})
		}
	}
	editor, err := s.plugins.OpenEditor(ctx, identity, kind, previous.State, overrides)
	if err != nil {
		return nil, err
	}
	defer editor.Close()
	ready()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-editor.Done():
		return nil, fmt.Errorf("插件窗口已关闭，工程参数保持原值")
	case <-capture:
	}
	blob, params, err := editor.Capture(ctx)
	if err != nil {
		return nil, err
	}
	previous.State = blob
	old := previous.Parameters
	previous.Parameters = nil
	for _, v := range params {
		previous.Parameters = append(previous.Parameters, project.PluginParameter{ID: v.ID, Name: v.Name, Default: v.Value, Value: v.Value})
	}
	for _, p := range old {
		if !slices.ContainsFunc(previous.Parameters, func(v project.PluginParameter) bool { return v.ID == p.ID }) {
			return nil, fmt.Errorf("编辑后的插件状态缺少参数 %s，原状态与自动化已保留", p.Name)
		}
	}
	return &previous, nil
}
