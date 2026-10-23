package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"icetudio/internal/app"
	"icetudio/internal/playback"
)

// PlaybackService controls the native engine; audio does not pass through Wails.
type PlaybackService struct{ svc *app.Service }

func (s *PlaybackService) Status() playback.Status         { return s.svc.PlaybackStatus() }
func (s *PlaybackService) Play(req playback.Request) error { return s.svc.PlayProject(req) }
func (s *PlaybackService) Stop() error                     { return s.svc.StopPlayback() }
func (s *PlaybackService) StopProject(id string) error     { return s.svc.StopProjectPlayback(id) }
func (s *PlaybackService) Seek(req playback.Request) error { return s.svc.SeekPlayback(req) }
func (s *PlaybackService) Configure(settings playback.Settings) error {
	return s.svc.ConfigureAudio(settings)
}
func (s *PlaybackService) PickSoundFont() (string, error) {
	return application.Get().Dialog.OpenFile().SetTitle("选择本地 SoundFont 音色").CanChooseFiles(true).AddFilter("SoundFont 音色", "*.sf2;*.sf3").PromptForSingleSelection()
}
