package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"icetudio/internal/agent"
	"icetudio/internal/config"
	"icetudio/internal/model"
	"icetudio/internal/storage"
)

// streamScriptModel implements StreamingChatModel: each turn emits scripted
// deltas before returning its merged response.
type streamScriptModel struct {
	steps []func(messages []model.Message, emit func(string) error) (model.ChatResponse, error)
	seen  [][]model.Message
	next  int
}

var _ agent.StreamingChatModel = (*streamScriptModel)(nil)

func (m *streamScriptModel) Chat(ctx context.Context, source config.Model, messages []model.Message, tools []model.Tool) (model.ChatResponse, error) {
	return m.ChatStream(ctx, source, messages, tools, nil)
}

func (m *streamScriptModel) ChatStream(_ context.Context, _ config.Model, messages []model.Message, _ []model.Tool, onDelta func(string) error) (model.ChatResponse, error) {
	shown := make([]model.Message, len(messages))
	copy(shown, messages)
	m.seen = append(m.seen, shown)
	if m.next >= len(m.steps) {
		return model.ChatResponse{}, errors.New("流式模型脚本用尽")
	}
	emit := func(string) error { return nil }
	if onDelta != nil {
		emit = onDelta
	}
	step := m.steps[m.next]
	m.next++
	return step(messages, emit)
}

func TestStreamDeltasBroadcastMergeAndClear(t *testing.T) {
	release := make(chan struct{})
	chat := &streamScriptModel{steps: []func([]model.Message, func(string) error) (model.ChatResponse, error){
		// Turn 1 requests a tool call; deltas for this turn exist briefly.
		func(_ []model.Message, emit func(string) error) (model.ChatResponse, error) {
			emit("我来查时间。")
			return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{toolCall("call-1")}}}, nil
		},
		// Turn 2 streams the final answer, then blocks until the test has
		// observed the uncommitted in-memory text.
		func(_ []model.Message, emit func(string) error) (model.ChatResponse, error) {
			emit("现在")
			emit("是 12:00")
			<-release
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "现在是 12:00"}}, nil
		},
	}}
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) { return "12:00", nil }}
	svc, _ := newTestService(t, chat, tools)

	var mu sync.Mutex
	var deltaEvents []Event
	stop := svc.Subscribe(func(evt Event) {
		mu.Lock()
		defer mu.Unlock()
		if evt.Type == "stream.delta" {
			deltaEvents = append(deltaEvents, evt)
		}
	})
	defer stop()

	session, err := svc.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	run, err := svc.StartConversationTask(session.ID, "几点了", nil)
	if err != nil {
		t.Fatal(err)
	}

	// While the run is blocked mid-stream, the uncommitted text is readable
	// through the reconnect surface.
	deadline := time.Now().Add(2 * time.Second)
	for {
		pending, err := svc.PendingStreamText(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pending == "现在是 12:00" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("in-memory stream not observable, last = %q", pending)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	waitForRun(t, svc, run.ID, "completed")

	mu.Lock()
	joined := ""
	for _, evt := range deltaEvents {
		if evt.RunID == run.ID {
			joined += evt.Detail
		}
	}
	mu.Unlock()
	if joined != "我来查时间。现在是 12:00" {
		t.Fatalf("broadcast deltas = %q", joined)
	}
	// After the run finished, everything is durable and the buffer is gone.
	if pending, _ := svc.PendingStreamText(run.ID); pending != "" {
		t.Fatalf("buffer survived run completion: %q", pending)
	}
	messages, err := svc.ListMessages(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	if last.Role != storage.RoleAssistant || last.Content != "现在是 12:00" {
		t.Fatalf("final message: %+v", last)
	}
}

// The streaming path of the guarded client enforces the exfiltration check
// before the adapter is reached, so a denied send never leaves the host.
func TestGuardedChatStreamBlocksOnCheckError(t *testing.T) {
	inner := &streamScriptModel{steps: []func([]model.Message, func(string) error) (model.ChatResponse, error){
		func(_ []model.Message, emit func(string) error) (model.ChatResponse, error) {
			emit("不应到达")
			return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "hi"}}, nil
		},
	}}
	blocked := guardedChat{inner: inner, check: func(context.Context) error { return errors.New("数据外发被拒绝") }}
	if _, err := blocked.ChatStream(context.Background(), config.Model{}, nil, nil, func(string) error { return nil }); err == nil {
		t.Fatal("streamed send accepted despite block")
	}
	allowed := guardedChat{inner: inner, check: func(context.Context) error { return nil }}
	var got strings.Builder
	if _, err := allowed.ChatStream(context.Background(), config.Model{}, nil, nil, func(text string) error { got.WriteString(text); return nil }); err != nil || got.String() != "不应到达" {
		t.Fatalf("allowed stream: %q %v", got.String(), err)
	}
}

func TestCredentialManagement(t *testing.T) {
	credPath := filepath.Join(t.TempDir(), "credentials.json")
	tools := &scriptTools{defs: timeToolDef(), respond: func(name, _ string) (string, error) { return "ok", nil }}
	chat := &scriptModel{}
	svc, _ := newTestService(t, chat, tools)
	svc.credStore = newCredentialStore(credPath)

	status, err := svc.ListCredentialStatus()
	if err != nil || len(status) != 3 {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	for _, item := range status {
		if item.Configured {
			t.Fatalf("unexpected configured provider: %+v", item)
		}
	}
	if err := svc.SaveCredential("openai", " sk-openai "); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveCredential("ollama", "nope"); err == nil {
		t.Fatal("local provider accepted")
	}
	if err := svc.SaveCredential("anthropic", "  "); err == nil {
		t.Fatal("empty key accepted")
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %v", info.Mode().Perm())
	}
	status, err = svc.ListCredentialStatus()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range status {
		if item.Provider == "openai" && !item.Configured {
			t.Fatal("openai not marked configured")
		}
		if item.Provider != "openai" && item.Configured {
			t.Fatalf("unexpected configured provider: %+v", item)
		}
	}
	if err := svc.DeleteCredential("openai"); err != nil {
		t.Fatal(err)
	}
	status, _ = svc.ListCredentialStatus()
	for _, item := range status {
		if item.Configured {
			t.Fatalf("provider still configured after delete: %+v", item)
		}
	}
}
