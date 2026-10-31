package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Name        string
	Description string
	Path        string
}

type Catalog struct {
	mu       sync.RWMutex
	items    map[string]Skill
	managed  map[string]Snapshot
	disabled map[string]bool
}

var validName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Discover reads only the standard SKILL.md metadata. Instructions are loaded on demand.
func Discover(root string) (*Catalog, error) {
	c := &Catalog{items: map[string]Skill{}}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		meta, _, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if meta.Name != entry.Name() {
			return nil, fmt.Errorf("%s: name 与目录名不一致", path)
		}
		c.items[meta.Name] = Skill{Name: meta.Name, Description: meta.Description, Path: path}
	}
	return c, nil
}

type metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parse(data []byte) (metadata, string, error) {
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return metadata{}, "", errors.New("缺少 SKILL.md YAML frontmatter")
	}
	parts := strings.SplitN(content[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return metadata{}, "", errors.New("缺少 SKILL.md YAML frontmatter 结束标记")
	}
	var meta metadata
	if err := yaml.Unmarshal([]byte(parts[0]), &meta); err != nil {
		return metadata{}, "", err
	}
	if len(meta.Name) > 64 || !validName.MatchString(meta.Name) || len(meta.Description) == 0 || len(meta.Description) > 1024 {
		return metadata{}, "", errors.New("SKILL.md name 或 description 不符合 Agent Skills 规范")
	}
	return meta, parts[1], nil
}

func (c *Catalog) Summary() string {
	var lines []string
	for _, item := range c.Items() {
		if c.Disabled(item.Name) {
			continue
		}
		lines = append(lines, "- "+item.Name+": "+item.Description)
	}
	return strings.Join(lines, "\n")
}

func (c *Catalog) SummaryFor(enabled []string) string {
	if c == nil || len(enabled) == 0 {
		return ""
	}
	var lines []string
	for _, name := range enabled {
		if item := c.Item(name); item != nil && !c.Disabled(name) {
			lines = append(lines, "- "+item.Name+": "+item.Description)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// SetDisabled is the reversible off switch for adopted or installed skills.
// Disabling is a user decision recorded by the app layer; unknown names are
// rejected so a stale row can never silently disable a future reinstall.
func (c *Catalog) SetDisabled(name string, disabled bool) error {
	if c == nil || c.Item(name) == nil {
		return fmt.Errorf("未知 Skill %q", name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disabled == nil {
		c.disabled = map[string]bool{}
	}
	if disabled {
		c.disabled[name] = true
	} else {
		delete(c.disabled, name)
	}
	return nil
}

// Disabled reports the overlay state; frozen run snapshots are unaffected by
// it because they were persisted at run creation.
func (c *Catalog) Disabled(name string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.disabled[name]
}

// Installed reports whether a skill with that name exists in the catalog;
// profile validation uses it to reject references to missing resources.
func (c *Catalog) Installed(name string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.items[name]
	return ok
}

// Item returns the metadata of one installed skill, or nil when absent.
func (c *Catalog) Item(name string) *Skill {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if item, ok := c.items[name]; ok {
		return &item
	}
	return nil
}

// Items lists every installed skill with its metadata, sorted by name.
// The profile share exporter walks the skill directories from these paths.
func (c *Catalog) Items() []Skill {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.items))
	for name := range c.items {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]Skill, 0, len(names))
	for _, name := range names {
		items = append(items, c.items[name])
	}
	return items
}

func (c *Catalog) Load(name string) (string, error) {
	if snap, ok := c.ManagedSnapshot(name); ok {
		return snap.Content, nil
	}
	item := c.Item(name)
	if item == nil {
		return "", fmt.Errorf("未知 Skill %q", name)
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return "", err
	}
	meta, _, err := parse(data)
	if err != nil || meta.Name != name {
		return "", errors.New("Skill 文件已损坏或名称已变更")
	}
	return string(data), nil
}
