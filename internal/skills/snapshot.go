package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const MaxSnapshotContent = 256 * 1024

// A snapshot has no filesystem identity: persisted content remains usable
// after an installed Skill is edited, moved or removed.
type Snapshot struct {
	Resources   map[string]Resource `json:"resources,omitempty"`
	Scopes      []string            `json:"scopes,omitempty"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Content     string              `json:"content"`
	SHA256      string              `json:"sha256"`
}

func (c *Catalog) Snapshot(all bool, names []string) ([]Snapshot, error) {
	if all {
		names = nil
		for _, item := range c.Items() {
			if c.Disabled(item.Name) {
				continue
			}
			names = append(names, item.Name)
		}
	}
	items := []Snapshot{}
	seen := map[string]bool{}
	total := 0
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		// A disabled skill is deliberately off: skipped for new freezes on
		// both the blanket and explicit paths instead of failing the run.
		if c.Disabled(name) {
			continue
		}
		item := c.Item(name)
		if item == nil {
			return nil, fmt.Errorf("缺少 Skill %q", name)
		}
		if snap, ok := c.ManagedSnapshot(name); ok {
			total += len(snap.Content)
			for _, r := range snap.Resources {
				total += len(r.Data)
			}
			if total > 16<<20 {
				return nil, fmt.Errorf("Skill 快照总量超过16MiB")
			}
			items = append(items, snap)
			continue
		}
		f, err := os.Open(item.Path)
		if err != nil {
			return nil, fmt.Errorf("冻结 Skill %q: %w", name, err)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, fmt.Errorf("Skill %q 必须是常规文件", name)
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxSnapshotContent+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > MaxSnapshotContent {
			return nil, fmt.Errorf("Skill %q 超过 256 KiB 快照上限", name)
		}
		resources, size, err := snapshotResources(filepath.Dir(item.Path))
		if err != nil {
			return nil, fmt.Errorf("冻结 Skill %q: %w", name, err)
		}
		total += len(data) + size
		if total > 16<<20 {
			return nil, fmt.Errorf("Skill 快照总量超过 16 MiB，请收窄选择")
		}
		meta, _, err := parse(data)
		if err != nil || meta.Name != name {
			return nil, fmt.Errorf("Skill %q 的内容或名称无效", name)
		}
		digest := sha256.Sum256(data)
		items = append(items, Snapshot{Resources: resources, Name: name, Description: meta.Description, Content: string(data), SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func SnapshotSummary(items []Snapshot) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item.Name+": "+item.Description)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
