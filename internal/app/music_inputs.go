package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"icetudio/internal/project"
)

const maxMusicInputsPayload = 4 << 20
const maxMusicInputsFile = 8 << 20

// MusicInputsRecord holds unapplied music form input drafts. They are UI
// recovery state, never music commands; recovery stays explicit in the
// frontend. Persisted in the Go data dir because the WebKit localStorage
// flush is not guaranteed before a native quit (found by native acceptance
// 2026-10-24: a draft preserved right before 保留草稿并退出 was lost).
type MusicInputsRecord struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	Payload string `json:"payload"`
}

type musicInputsShape struct {
	Version int `json:"version"`
	Entries []struct {
		Key     string `json:"key"`
		Raw     string `json:"raw"`
		Label   string `json:"label"`
		Title   string `json:"title"`
		Target  struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		} `json:"target"`
		TrackID string   `json:"trackID"`
		ClipID  string   `json:"clipID"`
		NoteIDs []string `json:"noteIDs"`
	} `json:"entries"`
}

// The frontend keeps the strict field-level validation; the service enforces
// the same structural bounds so a corrupted file cannot grow unbounded.
func validateMusicInputsPayload(payload string) error {
	if len(payload) > maxMusicInputsPayload {
		return errors.New("音乐输入草稿超过 4 MiB；原草稿未覆盖")
	}
	var shape musicInputsShape
	if err := strictDraftJSON(payload, &shape); err != nil {
		return err
	}
	if shape.Version != 1 {
		return errors.New("音乐输入草稿版本无效")
	}
	if len(shape.Entries) > 256 {
		return errors.New("音乐输入草稿超过 256 条")
	}
	for _, e := range shape.Entries {
		if e.Key == "" || len(e.Key) > 512 || e.Label == "" || len(e.Raw) > 1024 || !project.ValidID(e.Target.ID) || e.Target.Revision < 0 || len(e.Title) > 200 {
			return errors.New("音乐输入草稿条目无效")
		}
	}
	return nil
}

func (s *Service) musicInputsPath() (string, error) {
	path, err := s.projectRecoveryPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "music-inputs.json"), nil
}

// Caller holds projectRecoveryMu, shared with the pending-operation journal.
func (s *Service) readMusicInputs() (*MusicInputsRecord, error) {
	path, err := s.musicInputsPath()
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxMusicInputsFile {
		return nil, errors.New("音乐输入草稿不是受支持的普通文件或超过 8 MiB；文件已保留")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMusicInputsFile+1))
	if err != nil {
		return nil, err
	}
	var record MusicInputsRecord
	if len(data) > maxMusicInputsFile {
		return nil, errors.New("音乐输入草稿过大；文件已保留")
	}
	if err := strictDraftJSON(string(data), &record); err != nil {
		return nil, fmt.Errorf("音乐输入草稿无效，文件已保留：%w", err)
	}
	if record.Version != 1 || !draftTokenValid(record.Token) {
		return nil, errors.New("音乐输入草稿版本或 token 无效；文件已保留")
	}
	if record.Payload != "" {
		if err := validateMusicInputsPayload(record.Payload); err != nil {
			return nil, fmt.Errorf("音乐输入草稿无效，文件已保留：%w", err)
		}
	}
	return &record, nil
}

func (s *Service) GetMusicInputs() (*MusicInputsRecord, error) {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	record, err := s.readMusicInputs()
	if err != nil || record == nil {
		return record, err
	}
	path, _ := s.musicInputsPath()
	if err := syncRecoveryDir(path); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Service) SaveMusicInputs(expectedToken, token, payload string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	if !draftTokenValid(token) {
		return errors.New("音乐输入草稿请求 token 无效")
	}
	if payload == "" {
		return errors.New("音乐输入草稿内容不能为空")
	}
	if err := validateMusicInputsPayload(payload); err != nil {
		return err
	}
	existing, err := s.readMusicInputs()
	if err != nil {
		return err
	}
	path, _ := s.musicInputsPath()
	if existing != nil && existing.Token == token && existing.Payload == payload {
		return syncRecoveryDir(path)
	}
	if (existing == nil && expectedToken != "") || (existing != nil && (existing.Token != expectedToken || existing.Token == token)) {
		return errors.New("音乐输入草稿已变化；未覆盖其他草稿，请重新读取")
	}
	return s.writeMusicInputs(MusicInputsRecord{Version: 1, Token: token, Payload: payload})
}

func (s *Service) writeMusicInputs(record MusicInputsRecord) error {
	path, err := s.musicInputsPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > maxMusicInputsFile {
		return errors.New("音乐输入草稿文件超过 8 MiB；原文件未覆盖")
	}
	if err := os.MkdirAll(s.dataDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dataDir, ".music-inputs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncRecoveryDir(path)
}

func (s *Service) ClearMusicInputs(expectedToken, token string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	if !draftTokenValid(token) {
		return errors.New("音乐输入草稿 token 无效")
	}
	existing, err := s.readMusicInputs()
	if err != nil {
		return err
	}
	path, _ := s.musicInputsPath()
	if existing == nil {
		return nil
	}
	if existing.Token != expectedToken || existing.Token == token {
		return errors.New("音乐输入草稿已变化；未清除新草稿")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncRecoveryDir(path)
}
