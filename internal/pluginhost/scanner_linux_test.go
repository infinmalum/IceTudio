//go:build linux

package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIsolatedScannerRealPluginMatrix(t *testing.T) {
	if os.Getenv("ICETUDIO_PLUGIN_TEST") != "1" {
		t.Skip("requires built scanner, approved SDKs and local real plugin fixtures")
	}
	base, err := filepath.Abs("../../bin")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	helper := filepath.Join(base, "icetudio-plugin-scan")
	setup := func(ctx context.Context) (scanModule, error) { return prepareScannerAt(ctx, helper) }
	c := New(t.TempDir())
	crashDir := t.TempDir()
	path := filepath.Join(crashDir, "crash.clap")
	crashFixture, err := os.ReadFile(filepath.Join(base, "e6-deps", "build", "scan-fault.clap"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, crashFixture, 0600); err != nil {
		t.Fatal(err)
	}
	timeout := filepath.Join(base, "e6-deps", "build", "scan-timeout.clap")
	paths := []string{path, timeout}
	for _, folder := range []string{"dexed", "surge"} {
		found, err := discover(ctx, filepath.Join(base, "e6-deps", folder))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, found...)
	}
	if err := c.scan(ctx, paths, false, nil, setup); err != nil {
		t.Fatal(err)
	}
	items, err := New(c.dir).List()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"Dexed": "instrument", "Surge XT": "instrument", "Surge XT Effects": "effect"}
	counts := map[string]int{}
	for _, module := range items {
		if module.Path == timeout {
			if module.Status != Failed || !strings.Contains(module.Detail, "超时") {
				t.Fatalf("timeout was not recorded: %+v", module)
			}
			continue
		}
		if module.Path == path {
			if module.Status != Failed || !strings.Contains(module.Detail, "异常退出") {
				t.Fatalf("crash was not recorded: %+v", module)
			}
			continue
		}
		if module.Status != Ready || len(module.Classes) != 1 {
			t.Fatalf("plugin not listed: %+v", module)
		}
		class := module.Classes[0]
		if class.Kind != expected[class.Name] || class.Version == "" || class.Vendor == "" {
			t.Fatalf("bad plugin metadata: %+v", class)
		}
		counts[class.Name]++
		t.Logf("%s %s %s %s", module.Format, class.Vendor, class.Name, class.Version)
	}
	if len(items) != 8 || len(counts) != 3 || counts["Dexed"] != 2 || counts["Surge XT"] != 2 || counts["Surge XT Effects"] != 2 {
		t.Fatalf("wrong plugin matrix: %+v", items)
	}
	called := false
	if err := c.scan(ctx, paths, false, nil, func(context.Context) (scanModule, error) {
		called = true
		return nil, nil
	}); err != nil || called {
		t.Fatalf("unchanged module was rescanned: %v", err)
	}
	replacement, err := os.ReadFile(filepath.Join(base, "e6-deps", "dexed", "Dexed.clap"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.scan(ctx, []string{path}, true, nil, setup); err != nil {
		t.Fatal(err)
	}
	reloaded, err := c.List()
	i := slices.IndexFunc(reloaded, func(module Module) bool { return module.Path == path })
	if err != nil || i < 0 || reloaded[i].Status != Ready || reloaded[i].Classes[0].Name != "Dexed" {
		t.Fatalf("explicit retry did not replace the crash record: %+v %v", reloaded, err)
	}
}
