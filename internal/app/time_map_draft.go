package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"icetudio/internal/project"
)

const maxTimeMapPayload = 4 << 20
const maxTimeMapFile = 8 << 20

// TimeMapDraftRecord is UI recovery state, never a music command.
type TimeMapDraftRecord struct {
	Version int    `json:"version"`
	Token   string `json:"token"`
	Payload string `json:"payload"`
}

type timeMapTempoRow struct {
	Key         int    `json:"key"`
	Tick        string `json:"tick"`
	BPM         string `json:"bpm"`
	OriginalBPM string `json:"originalBPM"`
	OriginalUS  int    `json:"originalUS"`
}
type timeMapMeterRow struct {
	Key         int    `json:"key"`
	Tick        string `json:"tick"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}
type timeMapSnapshot struct {
	Version      int                  `json:"version"`
	ProjectID    string               `json:"projectID"`
	Title        string               `json:"title"`
	PPQ          int                  `json:"ppq"`
	BaseRevision int64                `json:"baseRevision"`
	Tempo        []project.TempoPoint `json:"tempo"`
	Meter        []project.MeterPoint `json:"meter"`
	Draft        struct {
		Tempo []timeMapTempoRow `json:"tempo"`
		Meter []timeMapMeterRow `json:"meter"`
	} `json:"draft"`
}

func strictDraftJSON(raw string, value any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("草稿包含尾随内容")
	}
	return nil
}
func draftTokenValid(token string) bool {
	return token != "" && len(token) <= 128 && !strings.ContainsAny(token, "\x00\r\n")
}
func validateTimeMapPayload(payload string) error {
	if len(payload) > maxTimeMapPayload {
		return errors.New("时间图草稿超过 4 MiB；原草稿未覆盖")
	}
	var snap timeMapSnapshot
	if err := strictDraftJSON(payload, &snap); err != nil {
		return err
	}
	if snap.Version != 1 || !project.ValidID(snap.ProjectID) || snap.PPQ < 1 || snap.PPQ > project.MaxPPQ || snap.BaseRevision < 0 || snap.BaseRevision > 1<<53-1 || len([]rune(snap.Title)) > 200 {
		return errors.New("时间图草稿身份或版本无效")
	}
	if len(snap.Tempo) > 100000 || len(snap.Meter) > 100000 {
		return errors.New("原时间图超过草稿行数上限")
	}
	st, _ := project.Blank("", snap.PPQ)
	if _, _, err := project.ApplyTransaction(&st, []project.Op{{Kind: "tempo.set", Tempo: snap.Tempo}, {Kind: "meter.set", Meter: snap.Meter}}); err != nil {
		return fmt.Errorf("原时间图无效：%w", err)
	}
	if !reflect.DeepEqual(st.Tempo, snap.Tempo) || !reflect.DeepEqual(st.Meter, snap.Meter) {
		return errors.New("原时间图必须排序且无重复")
	}
	stringsValid := func(values ...string) bool {
		for _, v := range values {
			if len(v) > 128 {
				return false
			}
		}
		return true
	}
	keys := map[int]bool{}
	if len(snap.Draft.Tempo) < 1 || len(snap.Draft.Meter) < 1 || len(snap.Draft.Tempo) > 100000 || len(snap.Draft.Meter) > 100000 {
		return errors.New("时间图草稿行数无效")
	}
	for _, r := range snap.Draft.Tempo {
		if r.Key < 0 || r.Key > 1<<31-1 || keys[r.Key] || r.OriginalUS < 1 || r.OriginalUS > 0xffffff || !stringsValid(r.Tick, r.BPM, r.OriginalBPM) {
			return errors.New("速度草稿行结构无效")
		}
		original, err := strconv.ParseFloat(r.OriginalBPM, 64)
		if err != nil || math.IsNaN(original) || math.IsInf(original, 0) || original <= 0 || math.Round(60000000/original) != float64(r.OriginalUS) {
			return errors.New("速度草稿的原显示值与原微秒值不一致")
		}
		keys[r.Key] = true
	}
	if snap.Draft.Tempo[0].Key != 0 {
		return errors.New("速度草稿缺少首行")
	}
	keys = map[int]bool{}
	for _, r := range snap.Draft.Meter {
		if r.Key < 0 || r.Key > 1<<31-1 || keys[r.Key] || !stringsValid(r.Tick, r.Numerator, r.Denominator) {
			return errors.New("拍号草稿行结构无效")
		}
		keys[r.Key] = true
	}
	if snap.Draft.Meter[0].Key != 0 {
		return errors.New("拍号草稿缺少首行")
	}
	return nil
}
func (s *Service) timeMapDraftPath() (string, error) {
	path, err := s.projectRecoveryPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "time-map-draft.json"), nil
}

// Caller holds projectRecoveryMu, shared with the pending-operation journal.
func (s *Service) readTimeMapDraft() (*TimeMapDraftRecord, error) {
	path, err := s.timeMapDraftPath()
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
	if !info.Mode().IsRegular() || info.Size() > maxTimeMapFile {
		return nil, errors.New("时间图草稿不是受支持的普通文件或超过 8 MiB；文件已保留")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTimeMapFile+1))
	if err != nil {
		return nil, err
	}
	var record TimeMapDraftRecord
	if len(data) > maxTimeMapFile {
		return nil, errors.New("时间图草稿过大；文件已保留")
	}
	if err := strictDraftJSON(string(data), &record); err != nil {
		return nil, fmt.Errorf("时间图草稿无效，文件已保留：%w", err)
	}
	if record.Version != 1 || !draftTokenValid(record.Token) {
		return nil, errors.New("时间图草稿版本或 token 无效；文件已保留")
	}
	if record.Payload != "" {
		if err := validateTimeMapPayload(record.Payload); err != nil {
			return nil, fmt.Errorf("时间图草稿无效，文件已保留：%w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	return &record, nil
}
func (s *Service) GetTimeMapDraft() (*TimeMapDraftRecord, error) {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	record, err := s.readTimeMapDraft()
	if err != nil || record == nil {
		return record, err
	}
	path, _ := s.timeMapDraftPath()
	if err := syncRecoveryDir(path); err != nil {
		return nil, err
	}
	return record, nil
}
func (s *Service) SaveTimeMapDraft(expectedToken, token, payload string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	if !draftTokenValid(token) {
		return errors.New("草稿请求 token 无效")
	}
	if err := validateTimeMapPayload(payload); err != nil {
		return err
	}
	existing, err := s.readTimeMapDraft()
	if err != nil {
		return err
	}
	path, _ := s.timeMapDraftPath()
	if existing != nil && existing.Token == token && existing.Payload == payload {
		return syncRecoveryDir(path)
	}
	if (existing == nil && expectedToken != "") || (existing != nil && (existing.Token != expectedToken || existing.Token == token)) {
		return errors.New("时间图草稿已变化；未覆盖其他草稿，请重新读取")
	}
	if existing != nil && existing.Payload != "" {
		var old, next timeMapSnapshot
		_ = json.Unmarshal([]byte(existing.Payload), &old)
		_ = json.Unmarshal([]byte(payload), &next)
		if old.ProjectID != next.ProjectID {
			return errors.New("另一工程已有时间图草稿，请先恢复或明确丢弃")
		}
	}
	return s.writeTimeMapDraft(TimeMapDraftRecord{Version: 1, Token: token, Payload: payload})
}

// Retain a token after discard to reject delayed initial saves (ABA).
func (s *Service) writeTimeMapDraft(record TimeMapDraftRecord) error {
	path, err := s.timeMapDraftPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > maxTimeMapFile {
		return errors.New("时间图草稿文件超过 8 MiB；原文件未覆盖")
	}
	if err := os.MkdirAll(s.dataDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dataDir, ".time-map-draft-*")
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
func (s *Service) ClearTimeMapDraft(expectedToken, token string) error {
	s.projectRecoveryMu.Lock()
	defer s.projectRecoveryMu.Unlock()
	if !draftTokenValid(token) {
		return errors.New("草稿 token 无效")
	}
	existing, err := s.readTimeMapDraft()
	if err != nil {
		return err
	}
	path, _ := s.timeMapDraftPath()
	if existing != nil && existing.Token == token && existing.Payload == "" {
		return syncRecoveryDir(path)
	}
	if (existing == nil && expectedToken != "") || (existing != nil && (existing.Token != expectedToken || existing.Token == token)) {
		return errors.New("时间图草稿已变化；未清除新草稿")
	}
	return s.writeTimeMapDraft(TimeMapDraftRecord{Version: 1, Token: token})
}
