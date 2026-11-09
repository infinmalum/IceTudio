package app

import (
	"context"
	"fmt"

	"icetudio/internal/audioasset"
	"icetudio/internal/playback"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func (s *Service) PrepareAudioAsset(ctx context.Context, path string) (project.AudioAsset, error) {
	return audioasset.Import(ctx, s.dataDir, path)
}

func (s *Service) ExportProjectAudio(ctx context.Context, request playback.ExportRequest, path string) error {
	if s.playback == nil {
		return fmt.Errorf("此宿主未启用原生渲染")
	}
	return s.playback.Export(ctx, request, path)
}

func (s *Service) ExportAudioBundle(ctx context.Context, id string, rev int64, path string) error {
	p, err := s.GetProject(id)
	if err != nil {
		return err
	}
	if p.Revision != rev {
		return fmt.Errorf("工程修订已变化")
	}
	return audioasset.ExportBundle(ctx, s.dataDir, path, p.State)
}

func (s *Service) ImportAudioBundle(ctx context.Context, path string) (storage.Project, error) {
	if s.store == nil {
		return storage.Project{}, fmt.Errorf("工程存储不可用")
	}
	state, err := audioasset.ImportBundle(ctx, s.dataDir, path)
	if err != nil {
		return storage.Project{}, err
	}
	p, err := s.store.CreateProjectFromState(ctx, state)
	if err == nil {
		s.publish(Event{Type: "project.changed", ProjectID: p.ID, Revision: p.Revision})
	}
	return p, err
}
