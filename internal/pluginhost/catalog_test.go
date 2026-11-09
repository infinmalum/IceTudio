package pluginhost

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRecoverySkipsUnchangedFailureAndRetriesExplicitly(t *testing.T) {
	ctx := context.Background()
	c := New(t.TempDir())
	path := filepath.Join(t.TempDir(), "plugin.clap")
	writeFixture(t, path, "first")
	good := []Class{{ID: "fixture", Name: "Instrument", Kind: "instrument"}}
	calls := 0
	setup := func(context.Context) (scanModule, error) {
		return func(_ context.Context, m Module) ([]Class, string, error) {
			calls++
			pending, err := New(c.dir).List()
			if err != nil || len(pending) != 1 || pending[0].Status != Interrupted || pending[0].SHA256 != m.SHA256 {
				t.Fatalf("missing durable scan checkpoint: %+v %v", pending, err)
			}
			if calls == 1 {
				return nil, "crashed", nil
			}
			return good, "", nil
		}, nil
	}
	scan := func(retry bool) {
		t.Helper()
		if err := c.scan(ctx, []string{path}, retry, nil, setup); err != nil {
			t.Fatal(err)
		}
	}
	scan(false)
	failed, err := c.List()
	if err != nil || len(failed) != 1 || failed[0].Status != Failed || failed[0].Detail != "crashed" {
		t.Fatalf("wrong failure record: %+v %v", failed, err)
	}
	c = New(c.dir)
	scan(false)
	if calls != 1 {
		t.Fatal("unchanged crashing module was replayed")
	}
	scan(true)
	ready, err := c.List()
	if err != nil || len(ready) != 1 || ready[0].Status != Ready || !slices.Equal(ready[0].Classes, good) {
		t.Fatalf("wrong retry result: %+v %v", ready, err)
	}
	writeFixture(t, path, "replacement")
	scan(false)
	changed, err := c.List()
	if err != nil || calls != 3 || changed[0].SHA256 == ready[0].SHA256 {
		t.Fatalf("replacement was not detected: %+v %v", changed, err)
	}
}

func TestCatalogCancelAndWorkerSetupFailureDoNotPublishResults(t *testing.T) {
	c := New(t.TempDir())
	path := filepath.Join(t.TempDir(), "plugin.clap")
	writeFixture(t, path, "first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setup := func(context.Context) (scanModule, error) {
		return func(context.Context, Module) ([]Class, string, error) {
			cancel()
			return nil, "ignore stale response", nil
		}, nil
	}
	if err := c.scan(ctx, []string{path}, false, nil, setup); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if items, err := c.List(); len(items) != 0 || err != nil {
		t.Fatalf("canceled module was published: %+v %v", items, err)
	}
	unavailable := errors.New("isolation unavailable")
	setup = func(context.Context) (scanModule, error) { return nil, unavailable }
	if err := c.scan(context.Background(), []string{path}, false, nil, setup); !errors.Is(err, unavailable) {
		t.Fatal(err)
	}
	if items, err := c.List(); len(items) != 0 || err != nil {
		t.Fatalf("infrastructure failure was published as plugin failure: %+v %v", items, err)
	}
}

func TestScanResponseRejectsDuplicateClassesAndUnsafeText(t *testing.T) {
	record := func(id, name string) string {
		return "plugin\t" + hex.EncodeToString([]byte(id)) + "\t" + hex.EncodeToString([]byte(name)) +
			"\t\t\t" + hex.EncodeToString([]byte("effect")) + "\n"
	}
	header := protocol + "\n"
	if classes, problem, err := parseResponse([]byte(header + record("fx", "中文效果器"))); err != nil ||
		problem != "" || len(classes) != 1 || classes[0].Name != "中文效果器" {
		t.Fatalf("wrong response: %+v %q %v", classes, problem, err)
	}
	for _, malformed := range []string{
		header, header + record("fx", "First") + record("fx", "Second"),
		header + record("fx", "Bad\nName"), header + record("fx", strings.Repeat("x", 257)),
		header + "error\tabc\n",
	} {
		if _, _, err := parseResponse([]byte(malformed)); err == nil {
			t.Fatalf("accepted invalid report %q", malformed)
		}
	}
}
