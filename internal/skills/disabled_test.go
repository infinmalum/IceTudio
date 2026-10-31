package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const disabledTestSkill = "---\nname: temp-skill\ndescription: Temporary helper.\n---\nBody.\n"

func newDisabledCatalog(t *testing.T) *Catalog {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "temp-skill")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(disabledTestSkill), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// Disabling is a reversible overlay: the skill stays installed and loadable
// for audit, but new freezes skip it on both selection paths and summaries
// stop offering it.
func TestDisabledSkillSkipsNewFreezesButKeepsInstall(t *testing.T) {
	c := newDisabledCatalog(t)
	if !c.Installed("temp-skill") || c.Disabled("temp-skill") {
		t.Fatal("fixture state wrong")
	}
	all, err := c.Snapshot(true, nil)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	if err = c.SetDisabled("temp-skill", true); err != nil {
		t.Fatal(err)
	}
	if err = c.SetDisabled("missing-skill", true); err == nil {
		t.Fatal("unknown name accepted")
	}
	all, err = c.Snapshot(true, nil)
	if err != nil || len(all) != 0 {
		t.Fatal("blanket freeze offered disabled skill", all, err)
	}
	explicit, err := c.Snapshot(false, []string{"temp-skill"})
	if err != nil || len(explicit) != 0 {
		t.Fatal("explicit freeze offered disabled skill", explicit, err)
	}
	if got := c.Summary(); strings.Contains(got, "temp-skill") {
		t.Fatal("summary still offers disabled skill", got)
	}
	if got := c.SummaryFor([]string{"temp-skill"}); got != "" {
		t.Fatal("enabled summary still offers disabled skill", got)
	}
	if !c.Installed("temp-skill") || c.Item("temp-skill") == nil {
		t.Fatal("disable removed the install")
	}
	if _, err = c.Load("temp-skill"); err != nil {
		t.Fatal("disable broke load", err)
	}
	if err = c.SetDisabled("temp-skill", false); err != nil {
		t.Fatal(err)
	}
	all, err = c.Snapshot(true, nil)
	if err != nil || len(all) != 1 {
		t.Fatal("re-enable did not restore freezing", all, err)
	}
}
