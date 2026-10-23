package app

import (
	"errors"
	"icetudio/internal/playback"
)

func (s *Service) PlaybackStatus() playback.Status {
	if s.playback == nil {
		return playback.Status{Error: "此宿主未启用播放服务；音乐编辑和导出仍可使用"}
	}
	return s.playback.Status()
}
func (s *Service) PlayProject(req playback.Request) error {
	if s.playback == nil {
		return errors.New("此宿主未启用原生播放")
	}
	return s.playback.Play(req)
}
func (s *Service) StopPlayback() error {
	if s.playback == nil {
		return nil
	}
	s.playback.Stop()
	return nil
}
func (s *Service) StopProjectPlayback(id string) error {
	if s.playback != nil {
		s.playback.StopProject(id)
	}
	return nil
}
func (s *Service) SeekPlayback(req playback.Request) error {
	if s.playback == nil {
		return errors.New("此宿主未启用原生播放")
	}
	return s.playback.Seek(req)
}
func (s *Service) ConfigureAudio(settings playback.Settings) error {
	if s.playback == nil {
		return errors.New("此宿主未启用原生播放")
	}
	return s.playback.Configure(settings)
}
