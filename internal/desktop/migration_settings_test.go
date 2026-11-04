package desktop

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/storage"
)

func TestNativeTypedExecutionEditorPreservesFullContract(t *testing.T) {
	next := storage.RuntimeConfig{ID: "default", Revision: 1, Name: "完整执行配置", Data: storage.BlankRuntimeConfig()}
	next.Data.Execution.Stages = []storage.TaskSpec{{ID: "step1", Task: "保留固定阶段", Worker: "worker1", AccessMode: "write", Acceptance: "可执行验收"}}
	next.Data.Execution.WorkerSources = []storage.WorkerSource{{Name: "worker1", Configuration: &storage.WorkerConfiguration{Name: "独立协作", ContextBudget: 8192}}}
	next.Data.Execution.Planner.SystemPrompt = "保留规划指令"
	next.Data.Execution.Improver.SystemPrompt = "保留改进指令"
	f := newNativeForm(&next)
	v := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(workspaceTheme())
		ui.Scroll(c).Fill().Children(func() { ui.Column(c).FillWidth().Children(func() { f.draw(c, false) }) })
	}, 700, 1000)
	text := strings.Join(v.Texts(), " ")
	for _, want := range []string{"执行组件", "任务预算", "独立协作配置", "规划", "方法改进", "验收标准"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if f.dirty || len(f.errors) != 0 || next.Data.Execution.Stages[0].ID != "step1" || next.Data.Execution.WorkerSources[0].Configuration == nil {
		t.Fatal("drawing mutated execution configuration")
	}
}
func TestNativePermissionMergeRetainsUneditedNewGrantsAndRejectsConflicts(t *testing.T) {
	base := storage.ContextPolicy{ApprovalMode: "manual", Permissions: storage.PermissionScope{WorkspacePaths: []string{"/music"}}}
	edited := cloneNative(base)
	edited.ApprovalMode = "semi"
	current := cloneNative(base)
	current.Permissions.Commands = []string{"ffmpeg"}
	got, err := mergeNativePolicy(base, edited, current)
	if err != nil || got.ApprovalMode != "semi" || !reflect.DeepEqual(got.Permissions.Commands, current.Permissions.Commands) {
		t.Fatalf("merge %+v %v", got, err)
	}
	current.ApprovalMode = "automatic"
	if _, err := mergeNativePolicy(base, edited, current); err == nil {
		t.Fatal("concurrent permission edit overwritten")
	}
}
func TestNativePreferencesPersistAndBrowserMapping(t *testing.T) {
	dir := t.TempDir()
	p := defaultPreferences()
	p.Theme = "light"
	p.Scale = 1.25
	p.LastProject = "p_native"
	p.EditorShare = 67
	if err := writePreferences(dir, p); err != nil {
		t.Fatal(err)
	}
	got, err := readPreferences(dir)
	if err != nil || got != p {
		t.Fatalf("prefs %+v %v", got, err)
	}
	mapped := preferencesFromBrowser(defaultPreferences(), map[string]string{"icetudio.theme": "light", "icetudio.ui-scale": "1.4", "icetudio.browser-width": "280px", "icetudio.lastProject": "p_original"})
	if mapped.Theme != "light" || mapped.Scale != 1.4 || mapped.BrowserWidth != 280 || mapped.LastProject != "p_original" {
		t.Fatal("browser preferences not mapped")
	}
	path := filepath.Join(dir, "native-ui.json")
	os.WriteFile(path, []byte(`{"version":99}`), 0600)
	if _, err := readPreferences(dir); err == nil {
		t.Fatal("bad preferences replaced")
	}
}
func TestBrowserImportPreservesConflictsAndIsIdempotent(t *testing.T) {
	_, svc, _ := testEdits(t)
	data := &browserDraftExport{Version: 1, Text: map[string]string{"new": "未发送输入"}}
	if count, err := importBrowserDrafts(svc, data); err != nil || count != 1 {
		t.Fatalf("first import %d %v", count, err)
	}
	if count, err := importBrowserDrafts(svc, data); err != nil || count != 0 {
		t.Fatalf("reimport %d %v", count, err)
	}
	data.Text["new"] = "不能覆盖"
	if _, err := importBrowserDrafts(svc, data); err == nil {
		t.Fatal("Native draft overwritten")
	}
	entries, _ := svc.GetConversationDrafts()
	if entries["new"].Text != "未发送输入" {
		t.Fatal("conflict lost input")
	}
}
func TestActualWebKitProfileSnapshotExportsTextAndIndexedDBFiles(t *testing.T) {
	if os.Getenv("ICETUDIO_MYGO_NATIVE") != "1" {
		t.Skip("requires actual WebKit6 GUI and installed Python GI")
	}
	root := t.TempDir()
	if retained := os.Getenv("ICETUDIO_BROWSER_EVIDENCE"); retained != "" {
		root = retained
		os.MkdirAll(root, 0700)
	}
	profile := filepath.Join(root, "webview", "storage", "icetudio")
	os.MkdirAll(profile, 0700)
	script := string(browserExportScript)
	start := strings.Index(script, "script = r'''") + len("script = r'''")
	// Replace only the test script body, keeping the same WebKit origin/session
	// implementation used by the real reader. No user profile is used here.
	stop := strings.Index(script[start:], "'''\ndef loaded") + start
	if stop < start {
		t.Fatal("export script boundary missing")
	}
	seed := `localStorage.setItem('icetudio.draft.new','恢复测试中文');localStorage.setItem('icetudio.theme','light');const db=await new Promise((resolve,reject)=>{const r=indexedDB.open('icetudio-drafts',1);r.onupgradeneeded=()=>r.result.createObjectStore('attachments');r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error)});await new Promise((resolve,reject)=>{const tx=db.transaction('attachments','readwrite');tx.objectStore('attachments').put([new File(['附件原始字节'],'notes.txt',{type:'text/plain'})],'new');tx.oncomplete=resolve;tx.onerror=()=>reject(tx.error)});db.close();return JSON.stringify({version:1});`
	script = script[:start] + seed + script[stop:]
	path := filepath.Join(root, "seed.py")
	os.WriteFile(path, []byte(script), 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", path, profile, filepath.Join(root, "seed-cache"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed WebKit fixture: %v %s", err, out)
	}
	// The read-only snapshot must not change any bytes in the source profile.
	hashes := map[string][32]byte{}
	filepath.WalkDir(profile, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, e := os.ReadFile(path)
			if e == nil {
				hashes[path] = sha256.Sum256(b)
			}
		}
		return nil
	})
	data, err := readBrowserExport(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if data.Text["new"] != "恢复测试中文" || len(data.Attachments["new"]) != 1 || data.Preferences["icetudio.theme"] != "light" {
		t.Fatalf("missing browser draft fields: text=%d files=%d prefs=%d", len(data.Text), len(data.Attachments["new"]), len(data.Preferences))
	}
	for path, hash := range hashes {
		b, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(b) != hash {
			t.Fatal("source profile changed", path, err)
		}
	}
}
