package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"icetudio/internal/project"
)

// ProjectEditRecovery identifies an intention, never a replayable command.
// Only the transaction result in the project store establishes a commit.
type ProjectEditRecovery struct {
	Version      int    `json:"version"`
	ProjectID    string `json:"projectID"`
	OpID         string `json:"opID"`
	BaseRevision int64  `json:"baseRevision"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
}

func (r ProjectEditRecovery) valid() bool {
	return r.Version == 1 && project.ValidID(r.ProjectID) && len(r.OpID) > 0 && len(r.OpID) <= 128 &&
		!strings.ContainsAny(r.OpID, "\x00\r\n") && r.BaseRevision >= 0 &&
		(r.Kind == "edit" || r.Kind == "undo" || r.Kind == "redo")
}

func (s *Service) projectRecoveryPath() (string, error) {
	if s.initErr != nil {
		return "", fmt.Errorf("宿主未就绪，不能访问工程恢复记录：%w", s.initErr)
	}
	if s.store == nil {
		return "", errors.New("工程存储未就绪，不能访问恢复记录")
	}
	if s.dataDir == "" {
		return "", errors.New("未配置应用数据目录，无法启用工程提交恢复")
	}
	return filepath.Join(s.dataDir, "pending-project-edit.json"), nil
}

// Caller holds projectRecoveryMu. Never treat malformed data as no intention.
func (s *Service) readProjectRecovery() (*ProjectEditRecovery, error) {
	path, err := s.projectRecoveryPath()
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
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("工程恢复记录不是受支持的普通文件或超过 4096 字节；文件已保留")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	var record ProjectEditRecovery
	if err = d.Decode(&record); err != nil {
		return nil, fmt.Errorf("工程恢复记录无效，文件已保留：%w", err)
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !record.valid() {
		return nil, errors.New("工程恢复记录格式或版本不受支持，文件已保留")
	}
	return &record, nil
}

func (s *Service) GetPendingProjectEdit() (*ProjectEditRecovery, error) {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	return s.readProjectRecovery()
}

func syncRecoveryDir(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// BeginProjectEdit must finish before the GUI sends the music transaction.
// It does not reserve a project revision or bypass the normal edit checks.
func (s *Service) BeginProjectEdit(projectID, opID string, baseRevision int64, kind string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	record := ProjectEditRecovery{Version: 1, ProjectID: projectID, OpID: opID, BaseRevision: baseRevision, Kind: kind}
	if !record.valid() {
		return errors.New("工程恢复标记参数无效")
	}
	existing, err := s.readProjectRecovery()
	if err != nil {
		return err
	}
	path, _ := s.projectRecoveryPath()
	if existing != nil {
		if existing.ProjectID != projectID || existing.OpID != opID || existing.BaseRevision != baseRevision || existing.Kind != kind {
			return errors.New("已有工程操作尚未确认，请先查询原操作结果；恢复标记未覆盖")
		}
		return syncRecoveryDir(path)
	}
	item, err := s.GetProject(projectID)
	if err != nil {
		return err
	}
	// Display metadata only; keep even unusually long titles within the journal budget.
	title := []rune(item.State.Title)
	if len(title) > 200 {
		title = title[:200]
	}
	record.Title = string(title)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.dataDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dataDir, ".pending-project-edit-*")
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

// Matching clears are idempotent if the file is already absent. An old caller
// cannot erase a newer intention; no operation or project data is deleted here.
func (s *Service) ClearPendingProjectEdit(projectID, opID string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	existing, err := s.readProjectRecovery()
	if err != nil {
		return err
	}
	path, _ := s.projectRecoveryPath()
	if existing == nil {
		return syncRecoveryDir(path)
	}
	if existing.ProjectID != projectID || existing.OpID != opID {
		return errors.New("待确认工程操作已变化；未清除其他操作的恢复标记")
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncRecoveryDir(path)
}
