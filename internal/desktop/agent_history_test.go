package desktop

import (
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"icetudio/internal/storage"
)

func TestNativeChatPageMergeRetainsEarlierAndReplacesStreamedTail(t *testing.T) {
	old := []storage.Message{{Seq: 1, Content: "更早消息"}, {Seq: 2, Content: "旧尾部"}}
	next := []storage.Message{{Seq: 2, Content: "更新内容"}, {Seq: 3, Content: "最新消息"}}
	got := mergeChatMessages(old, next)
	if len(got) != 3 || got[0].Content != "更早消息" || got[1].Content != "更新内容" {
		t.Fatal("history merge lost earlier page or kept stale tail")
	}
	if !reflect.DeepEqual(mergeChatMessages(got, nil), got) {
		t.Fatal("empty refresh deleted history")
	}
}
func TestNativeAgentChineseMarkdownHasReachableHistoryAndTasks(t *testing.T) {
	a := Workspace{agent: agentHistory{selected: "conversation", conversations: []storage.Conversation{{ID: "conversation", Title: "中文对话"}}, messages: []storage.Message{{ID: "m_one", Role: "assistant", Content: "### 中文段落\n\n| 轨道 | 乐器 |\n| --- | --- |\n| 主旋律 | 钢琴 |\n\n```go\nservice.GetProject(id)\n```\n\n![仅描述，不下载](https://example.invalid/image.png)"}}, runs: []storage.Run{{ID: "run", Status: "succeeded"}}}}
	v := ui.NewTester(func(c *ui.Context) { c.SetTheme(workspaceTheme()); a.nativeAgentPanel(c) }, 500, 800)
	text := strings.Join(v.Texts(), " ")
	for _, want := range []string{"中文段落", "主旋律", "钢琴", "任务 · succeeded"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if a.md.LoadImage != nil {
		t.Fatal("Agent history must not load remote images")
	}
}
