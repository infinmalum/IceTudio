package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/config"
	"icetudio/internal/storage"
)

func uiQueue(t *testing.T, a *Workspace) func(func() bool) {
	t.Helper()
	jobs := make(chan func(), 512)
	a.dispatch = func(fn func()) { jobs <- fn }
	return func(done func() bool) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for !done() {
			select {
			case fn := <-jobs:
				fn()
			case <-timer.C:
				t.Fatal("Native UI service callback did not complete")
			}
		}
	}
}
func TestNativeModelFormSavesActualEndpointWithoutLeakingCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"native-model"}]}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial := config.DefaultModel("openai")
	initial.BaseURL = server.URL + "/v1"
	initial.Name = "native-model"
	svc := app.New(app.Options{Store: store, DataDir: dir, Config: config.Config{Model: initial}, CredentialPath: filepath.Join(dir, "credentials.json"), ConfigPath: filepath.Join(dir, "config.json")})
	a := Workspace{service: svc}
	pump := uiQueue(t, &a)
	a.openSettings("模型")
	pump(func() bool { return a.settings != nil && !a.settings.busy })
	d := a.settings
	d.model.Name = "另一个模型"
	d.apiKey = "native-test-secret"
	d.form.dirty = true
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.modelSettings(c, d) }, 720, 900)
	if err := v.Click("保存并检查连接"); err != nil {
		t.Fatal(err)
	}
	pump(func() bool { return !d.busy })
	saved, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil || saved.Model.BaseURL != initial.BaseURL || saved.Model.Name != "另一个模型" || d.form.dirty {
		t.Fatalf("Native form did not save selected endpoint: %+v %v", saved.Model, err)
	}
	for _, name := range []string{"config.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "native-test-secret") {
			t.Fatal("credential leaked into public model config")
		}
	}
	if d.apiKey != "" {
		t.Fatal("credential remained in Native form after save")
	}
	public, _ := json.Marshal(svc.ModelConfig())
	if strings.Contains(string(public), "native-test-secret") {
		t.Fatal("credential leaked into model DTO")
	}
}
func TestNativePolicyFormDoesNotOverwriteConcurrentFolderGrant(t *testing.T) {
	_, svc, _ := testEdits(t)
	conv, err := svc.CreateConversation("", "", "权限测试")
	if err != nil {
		t.Fatal(err)
	}
	a := Workspace{service: svc, agent: agentHistory{selected: conv.ID}}
	pump := uiQueue(t, &a)
	a.openSettings("权限")
	pump(func() bool { return !a.settings.busy })
	d := a.settings
	d.context.Policy.ApprovalMode = "manual"
	d.form.dirty = true
	latest, _ := svc.GetLocalContext(conv.ContextID)
	latest.Policy.Permissions.WorkspacePaths = []string{"/newly-authorized"}
	if _, err := svc.UpdateContextPolicy(conv.ContextID, latest.Policy); err != nil {
		t.Fatal(err)
	}
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.permissionSettings(c, d) }, 720, 1100)
	if err := v.Click("保存本机权限"); err != nil {
		t.Fatal(err)
	}
	pump(func() bool { return !d.busy })
	got, err := svc.GetLocalContext(conv.ContextID)
	if err != nil || got.Policy.ApprovalMode != "manual" || len(got.Policy.Permissions.WorkspacePaths) != 1 || got.Policy.Permissions.WorkspacePaths[0] != "/newly-authorized" {
		t.Fatalf("Native policy save overwrote a new grant: %+v %v", got, err)
	}
}
func TestNativeFeedbackRecordsOriginalQuoteWithOptionalRemark(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := app.New(app.Options{Store: store, DataDir: dir})
	conv, err := svc.CreateConversation("", "", "反馈")
	if err != nil {
		t.Fatal(err)
	}
	message, err := store.AddMessage(context.Background(), conv.ID, "", storage.RoleUser, "原话必须按逐对话保留")
	if err != nil {
		t.Fatal(err)
	}
	a := Workspace{service: svc, agent: agentHistory{selected: conv.ID}}
	pump := uiQueue(t, &a)
	a.openSkillPanel()
	pump(func() bool { return !a.skillPanel.busy })
	d := a.skillPanel
	d.messageID = message.ID
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.skillDialog(c) }, 1000, 1000)
	if err := v.Click("记录这条反馈"); err != nil {
		t.Fatal(err)
	}
	pump(func() bool { return !d.busy })
	notes, err := svc.ListFeedback(conv.ID)
	if err != nil || len(notes) != 1 || notes[0].Quote != message.Content || notes[0].Note != "" {
		t.Fatalf("feedback quote/optional note regression: %+v %v", notes, err)
	}
}
func TestNativeApprovalPresentationUsesConcreteObjectsAndFailsClosed(t *testing.T) {
	a := Workspace{approval: &nativeApproval{open: true, call: storage.ToolCall{ID: "pending", ToolName: "local__send_material", State: storage.CallPending, MaterialError: "无法读取原快照", Arguments: `{"materialId":"frozen"}`}}}
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.approvalDialog(c) }, 1100, 900)
	if !v.HasText("无法读取原快照") {
		t.Fatal("missing concrete preparation failure")
	}
	// A disabled allow action must not invoke the service (nil here).
	_ = v.Click("允许此次操作")
	if a.approval.busy {
		t.Fatal("missing material was allowed")
	}
	title, target, _ := approvalDescription(storage.ToolCall{ToolName: "local__write_file", Arguments: `{"path":"/music/render.mid","content":"x"}`})
	if title != "写入文件" || target != "/music/render.mid" {
		t.Fatal("approval hid its concrete file")
	}
}
func TestNativeTypedSettingsKeepInvalidRawInputAndFullRoleConfiguration(t *testing.T) {
	d := storage.BlankRuntimeConfig()
	d.Execution.Planner.SystemPrompt = "planner"
	d.Execution.Reviewer.SystemPrompt = "reviewer"
	d.Execution.Worker.SystemPrompt = "worker"
	d.Execution.Improver.SystemPrompt = "improver"
	before, _ := json.Marshal(d)
	f := newNativeForm(&d)
	ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); f.draw(c, false) }, 900, 1100)
	after, _ := json.Marshal(d)
	if string(before) != string(after) || f.dirty {
		t.Fatal("rendering changed execution capabilities")
	}
	if !strings.Contains(string(after), "planner") || !strings.Contains(string(after), "improver") {
		t.Fatal("role data lost")
	}
}
