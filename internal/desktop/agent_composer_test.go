package desktop

import (
	"context"
	"encoding/base64"
	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/skills"
	"icetudio/internal/storage"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/app"
	"icetudio/internal/materials"
)

func TestNativeComposerDraftsPersistCASAndRetainUnknownSend(t *testing.T) {
	svc, p := composerTestService(t, false)
	conv, err := svc.CreateConversation("", "", "待发送")
	if err != nil {
		t.Fatal(err)
	}
	d := app.ConversationDraft{Token: "first", Text: "保留中文\n第二行", Uploads: []materials.Upload{{Name: "notes.txt", Data: base64.StdEncoding.EncodeToString([]byte("原始附件"))}}}
	if err := svc.SaveConversationDraft(conv.ID, "", d); err != nil {
		t.Fatal(err)
	}
	entries, err := svc.GetConversationDrafts()
	if err != nil || !reflect.DeepEqual(entries[conv.ID], d) {
		t.Fatalf("read %v %v", entries, err)
	}
	if err := svc.SaveConversationDraft(conv.ID, "", app.ConversationDraft{Token: "stale", Text: "迟到"}); err == nil {
		t.Fatal("stale write accepted")
	}
	if err := svc.SetConversationProject(conv.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	conv, _ = svc.GetConversation(conv.ID)
	consent, err := svc.PreviewSend(conv.ID, true, "main")
	if err != nil {
		t.Fatal(err)
	}
	// This service intentionally has no Agent runtime. The dispatcher must
	// preserve the input and durable ambiguity marker, not consume or retry it.
	next, err := executeNativeSend(svc, &nativeSend{key: conv.ID, draft: d, conversation: conv, consent: consent, selection: nil}, d.Token)
	if err == nil || !next.Dispatching || next.Text != d.Text || !reflect.DeepEqual(next.Uploads, d.Uploads) {
		t.Fatalf("failed send lost input: %+v %v", next, err)
	}
	entries, _ = svc.GetConversationDrafts()
	if !reflect.DeepEqual(entries[conv.ID], next) {
		t.Fatal("unknown send not durable")
	}
	empty := app.ConversationDraft{Token: "consumed"}
	if err := svc.SaveConversationDraft(conv.ID, next.Token, empty); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveConversationDraft(conv.ID, "", d); err == nil {
		t.Fatal("ABA initial save resurrected consumed input")
	}
}
func TestNativeSendRebindingRequiresNewConfirmationBeforeAnyGrantOrDispatch(t *testing.T) {
	svc, p := composerTestService(t, false)
	conv, err := svc.CreateConversation("", "", "范围")
	if err != nil {
		t.Fatal(err)
	}
	d := app.ConversationDraft{Token: "draft", Text: "不要改变目标"}
	if err := svc.SaveConversationDraft(conv.ID, "", d); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewSend(conv.ID, false, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	next, err := executeNativeSend(svc, &nativeSend{key: conv.ID, conversation: conv, draft: d, consent: preview}, d.Token)
	if err == nil || next.Dispatching {
		t.Fatal("changed binding dispatched")
	}
	stored, _ := svc.GetConversationDrafts()
	if stored[conv.ID].Token != d.Token {
		t.Fatal("rejected confirmation consumed input")
	}
	if _, err := svc.StartConversationTaskForProject(conv.ID, "", "不要改目标", nil); err == nil || !strings.Contains(err.Error(), "绑定已变化") {
		t.Fatal("service accepted changed confirmation", err)
	}
	if _, err := svc.StartConversationTaskForProject(conv.ID, p.ID, "无模型", nil); err == nil || !strings.Contains(err.Error(), "Agent 尚未初始化") {
		t.Fatal("expected binding did not reach existing runtime", err)
	}
}
func TestNativeAttachmentSnapshotAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "材料.txt")
	os.WriteFile(path, []byte("读取时冻结"), 0600)
	uploads, err := readAgentAttachments([]string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("后续变化"), 0600)
	decoded, _ := base64.StdEncoding.DecodeString(uploads[0].Data)
	if string(decoded) != "读取时冻结" {
		t.Fatal("attachment was a path rather than a frozen snapshot")
	}
	if _, err := readAgentAttachments([]string{dir}, nil); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := readAgentAttachments(make([]string, 9), nil); err == nil {
		t.Fatal("attachment count ignored")
	}
	_, svc, _ := testEdits(t)
	if err := svc.SaveConversationDraft("new", "", app.ConversationDraft{Token: "too-long", Text: strings.Repeat("a", 10001)}); err == nil {
		t.Fatal("oversize draft accepted")
	}
}
func TestNativeComposerUIAndCloseState(t *testing.T) {
	a := Workspace{composer: agentComposer{ready: true, entries: map[string]app.ConversationDraft{"new": {Token: "one", Text: "未发送输入"}}, errors: map[string]string{}}}
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.nativeComposer(c) }, 500, 500)
	text := strings.Join(v.Texts(), " ")
	for _, want := range []string{"新对话", "附件", "发送前确认"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	if _, blocked := composerCloseState(&a.composer); !blocked {
		t.Fatal("draft bypassed close guard")
	}
	a.composer.entries["new"] = app.ConversationDraft{Token: "empty"}
	if _, blocked := composerCloseState(&a.composer); blocked {
		t.Fatal("empty tombstone blocked clean exit")
	}
	s := nativeSelection("p", "p", "c_original")
	other := nativeSelection("p", "other", "c_new")
	if s.Clips[0] != "c_original" || other != nil {
		t.Fatal("selection retargeted")
	}
}

func composerTestService(t *testing.T, runtime bool) (*app.Service, storage.Project) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	source := config.DefaultModel("lmstudio")
	source.Name = "test-model"
	opts := app.Options{Store: store, DataDir: dir, Config: config.Config{Model: source}}
	if runtime {
		catalog, err := skills.Discover(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		runs := agent.NewRuns(store)
		t.Cleanup(runs.Close)
		opts.Skills = catalog
		opts.Runs = runs
		opts.Tools = composerTestTools{}
		opts.Chat = composerTestChat{}
	}
	svc := app.New(opts)
	p, err := svc.CreateProject("发送工程", 480)
	if err != nil {
		t.Fatal(err)
	}
	return svc, p
}

type composerTestChat struct{}

func (composerTestChat) Chat(context.Context, config.Model, []model.Message, []model.Tool) (model.ChatResponse, error) {
	return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "本地测试回复"}}, nil
}

type composerTestTools struct{}

func (composerTestTools) Definitions(context.Context) ([]model.Tool, error)    { return nil, nil }
func (composerTestTools) Call(context.Context, string, string) (string, error) { return "", nil }
func TestNativeSendSuccessConsumesOnlyAcknowledgedDraftAndFreezesProject(t *testing.T) {
	svc, p := composerTestService(t, true)
	conv, err := svc.CreateConversation("", "", "发送")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetConversationProject(conv.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	conv, _ = svc.GetConversation(conv.ID)
	d := app.ConversationDraft{Token: "send-one", Text: "独立的音乐工程", Uploads: []materials.Upload{{Name: "notes.txt", Data: base64.StdEncoding.EncodeToString([]byte("冻结材料"))}}}
	if err := svc.SaveConversationDraft(conv.ID, "", d); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewSend(conv.ID, true, "main")
	if err != nil {
		t.Fatal(err)
	}
	n := &nativeSend{key: conv.ID, draft: d, conversation: conv, consent: preview}
	result, err := executeNativeSend(svc, n, d.Token)
	if err != nil {
		t.Fatal(err)
	}
	svc.WaitIdle()
	if result.RunID == "" || result.Dispatching || result.Text != "" || len(result.Uploads) != 0 {
		t.Fatalf("not consumed: %+v", result)
	}
	run, err := svc.GetRun(result.RunID)
	if err != nil || run.Status != "completed" {
		t.Fatalf("run %+v %v", run, err)
	}
	scope, err := storage.ParseProjectScope(run.ProjectScope)
	if err != nil || scope.ProjectID != p.ID {
		t.Fatalf("scope %+v %v", scope, err)
	}
	page, err := svc.ListChatMessages(conv.ID, 0, 80)
	if err != nil || len(page.Messages) != 2 || len(page.Messages[0].Attachments) != 1 {
		t.Fatalf("history %+v %v", page, err)
	}
	// A repeated click with the consumed token cannot create another run.
	if _, err := executeNativeSend(svc, n, d.Token); err == nil {
		t.Fatal("old confirmed draft dispatched twice")
	}
	runs, _ := svc.ListRuns(conv.ID)
	if len(runs) != 1 {
		t.Fatal("duplicate run", len(runs))
	}
	if err := svc.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetProject(p.ID); err != nil {
		t.Fatal("deleting conversation deleted music", err)
	}
}

func TestNativeComposerTextAreaEditsAreCapturedAndEnterDoesNotSend(t *testing.T) {
	a := Workspace{draftQueue: make(chan func(), 64), composer: agentComposer{ready: true, entries: map[string]app.ConversationDraft{}, errors: map[string]string{}}}
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.nativeComposer(c) }, 500, 500)
	if err := v.Click("助手消息"); err != nil {
		t.Fatal(err)
	}
	v.Type("中文输入")
	v.Key(0, ui.KeyEnter)
	v.Type("下一行")
	d := a.composer.entries["new"]
	if !strings.Contains(d.Text, "中文输入") || !strings.Contains(d.Text, "\n") || !strings.Contains(d.Text, "下一行") || a.composer.busy || d.Dispatching {
		t.Fatalf("multiline input/send semantics: %+v", d)
	}
	if a.composer.writes < 1 {
		t.Fatal("text input did not queue durable checkpoint")
	}
}
