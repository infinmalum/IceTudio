package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"icetudio/internal/storage"
)

func TestConversationDraftRestartCorruptionAndFileMode(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "music.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := New(Options{Store: store, DataDir: dir})
	d := ConversationDraft{Token: "saved", Text: "重启仍保留", Dispatching: true}
	if err := svc.SaveConversationDraft("new", "", d); err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Store: store, DataDir: dir})
	entries, err := restarted.GetConversationDrafts()
	if err != nil || !reflect.DeepEqual(entries["new"], d) {
		t.Fatalf("restart %v %v", entries, err)
	}
	path := filepath.Join(dir, "native-conversation-drafts.json")
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0077 != 0 {
		t.Fatal("private input file is readable by other users")
	}
	raw := []byte(`{"version":99,"entries":{}}`)
	os.WriteFile(path, raw, 0600)
	if _, err := svc.GetConversationDrafts(); err == nil {
		t.Fatal("unsupported version accepted")
	}
	if err := svc.SaveConversationDraft("new", "", ConversationDraft{Token: "replacement"}); err == nil {
		t.Fatal("bad file overwritten")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(raw) {
		t.Fatal("original bad file changed")
	}
	os.Remove(path)
	os.Symlink("missing-target", path)
	if _, err := svc.GetConversationDrafts(); err == nil || !strings.Contains(err.Error(), "文件类型") {
		t.Fatal("symlink accepted", err)
	}
}
