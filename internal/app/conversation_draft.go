package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"unicode/utf8"

	"icetudio/internal/materials"
	"icetudio/internal/project"
)

// ConversationDraft is Native composer recovery state, not a replayable task.
// Dispatching is durable before StartConversationTask; recovery must ask the
// user to inspect history rather than automatically resubmit unknown effects.
type ConversationDraft struct {
	Token       string             `json:"token"`
	Text        string             `json:"text"`
	Uploads     []materials.Upload `json:"uploads,omitempty"`
	Dispatching bool               `json:"dispatching,omitempty"`
	RunID       string             `json:"runID,omitempty"`
}
type conversationDraftFile struct {
	Version int                          `json:"version"`
	Entries map[string]ConversationDraft `json:"entries"`
}

const maxConversationDraftFile = 64 << 20

func validateConversationDraft(key string, d ConversationDraft) error {
	if key != "new" && !project.ValidID(key) {
		return errors.New("对话草稿目标无效")
	}
	if !draftTokenValid(d.Token) || !utf8.ValidString(d.Text) || len(d.Text) > 10000 || len(d.Uploads) > materials.MaxFiles {
		return errors.New("对话草稿文字或附件数量超过上限")
	}
	total := 0
	for _, u := range d.Uploads {
		if len(u.Name) > 240 || len(u.Data) > base64.StdEncoding.EncodedLen(materials.MaxFileBytes) {
			return errors.New("草稿附件超过上限")
		}
		raw, err := base64.StdEncoding.DecodeString(u.Data)
		if err != nil {
			return err
		}
		total += len(raw)
	}
	if total > materials.MaxTotalBytes {
		return errors.New("草稿附件总量超过 20 MiB")
	}
	if len(d.Uploads) > 0 {
		if _, err := materials.Prepare(d.Uploads); err != nil {
			return err
		}
	}
	if d.RunID != "" && !project.ValidID(d.RunID) {
		return errors.New("草稿任务身份无效")
	}
	return nil
}
func (s *Service) readConversationDrafts() (conversationDraftFile, string, error) {
	record := conversationDraftFile{Version: 1, Entries: map[string]ConversationDraft{}}
	recovery, err := s.projectRecoveryPath()
	if err != nil {
		return record, "", err
	}
	path := filepath.Join(filepath.Dir(recovery), "native-conversation-drafts.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return record, path, nil
	}
	if err != nil {
		return record, path, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxConversationDraftFile {
		return record, path, errors.New("Native 对话草稿文件类型或大小无效；原文件保留")
	}
	f, err := os.Open(path)
	if err != nil {
		return record, path, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConversationDraftFile+1))
	if err != nil {
		return record, path, err
	}
	if len(b) > maxConversationDraftFile {
		return record, path, errors.New("Native 对话草稿文件过大；原文件保留")
	}
	if err := strictDraftJSON(string(b), &record); err != nil {
		return record, path, fmt.Errorf("Native 对话草稿损坏；原文件保留：%w", err)
	}
	if record.Version != 1 || record.Entries == nil {
		return record, path, errors.New("Native 对话草稿版本无效；原文件保留")
	}
	for k, d := range record.Entries {
		if err := validateConversationDraft(k, d); err != nil {
			return record, path, err
		}
	}
	return record, path, nil
}
func (s *Service) GetConversationDrafts() (map[string]ConversationDraft, error) {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	r, _, err := s.readConversationDrafts()
	return r.Entries, err
}

// SaveConversationDraft replaces only one key. Empty records retain their token
// as an ABA barrier; neither conversation deletion nor send deletes music.
func (s *Service) SaveConversationDraft(key, expected string, next ConversationDraft) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	if err := validateConversationDraft(key, next); err != nil {
		return err
	}
	r, path, err := s.readConversationDrafts()
	if err != nil {
		return err
	}
	old := r.Entries[key]
	if old.Token == next.Token && reflect.DeepEqual(old, next) {
		return syncRecoveryDir(path)
	}
	if old.Token != expected || old.Token == next.Token {
		return errors.New("对话草稿已变化；未覆盖较新输入")
	}
	r.Entries[key] = next
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > maxConversationDraftFile {
		return errors.New("全部 Native 对话草稿超过 64 MiB；原文件保留")
	}
	if err = os.MkdirAll(s.dataDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dataDir, ".native-conversation-drafts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
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
