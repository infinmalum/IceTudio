package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"icetudio/internal/ipc"
	"icetudio/internal/storage"
)

func TestCLIConfigShowUpdateRestore(t *testing.T) {
	client, _ := newCLIHost(t, cliRetrospectiveMock{}, 0)

	if code := cmdConfig(client, []string{"list"}); code != 0 {
		t.Fatal(code)
	}
	if code := cmdConfig(client, []string{"show"}); code != 0 {
		t.Fatal(code)
	}
	if code := cmdConfig(client, []string{"show", "--id", "missing"}); code == 0 {
		t.Fatal("unknown config accepted")
	}

	// Show reads the stored document; edit it and submit it back.
	current := defaultConfig(t, client)
	current.Data.Description = "CLI 更新后的配置"
	file := filepath.Join(t.TempDir(), "config.json")
	raw, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdConfig(client, []string{"update", "--file", file}); code != 0 {
		t.Fatal(code)
	}

	// Submitting the same document again is refused on revision mismatch.
	if code := cmdConfig(client, []string{"update", "--file", file}); code == 0 {
		t.Fatal("stale revision accepted")
	}

	if code := cmdConfig(client, []string{"restore-default"}); code != 0 {
		t.Fatal(code)
	}
	restored := defaultConfig(t, client)
	if restored.Data.Description == "CLI 更新后的配置" || restored.Revision != current.Revision+2 {
		t.Fatalf("restore: %+v", restored)
	}
}

// defaultConfig fetches the built-in default config over IPC.
func defaultConfig(t *testing.T, client *ipc.Client) storage.RuntimeConfig {
	t.Helper()
	var config storage.RuntimeConfig
	if err := client.Call("configs.get", map[string]any{"id": storage.DefaultConfigID}, &config); err != nil {
		t.Fatal(err)
	}
	return config
}
