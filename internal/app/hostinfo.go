package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// SkillInfo is one installed skill's user-facing metadata: what the
// capability pickers list so users select installed skills instead of typing
// names. Managed marks skills published from AI drafts.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Managed     bool   `json:"managed"`
}

// ListSkills returns the installed skill catalog, sorted by name.
func (s *Service) ListSkills() ([]SkillInfo, error) {
	if s.skills == nil {
		return []SkillInfo{}, nil
	}
	items := s.skills.Items()
	out := make([]SkillInfo, 0, len(items))
	for _, item := range items {
		_, managed := s.skills.ManagedSnapshot(item.Name)
		out = append(out, SkillInfo{Name: item.Name, Description: item.Description, Managed: managed})
	}
	return out, nil
}

// ReadHostLogs returns up to limit recent lines of the host log, oldest
// first, so the desktop UI can offer the same troubleshooting view as
// `icetudio logs`.
func (s *Service) ReadHostLogs(limit int) ([]string, error) {
	if s.dataDir == "" {
		return nil, errors.New("数据目录未配置，无法读取日志")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	data, err := os.ReadFile(filepath.Join(s.dataDir, "icetudio.log"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []string{}, nil
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, nil
}
