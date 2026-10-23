package storage

import (
	"context"

	"testing"
)

// assistantData mirrors the former built-in assistant fixture: direct
// workflow, on-demand review, improver enabled, local MCP server selected.
func assistantData() RuntimeConfigData {
	d := BlankRuntimeConfig()
	d.Description = "测试执行配置"
	d.EnabledMCPServers = []string{"local"}
	d.Execution.Review.Mode = "on_demand"
	d.Execution.ImproverEnabled = true
	return d
}

func TestConversationIsolationAndSelection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "日常", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateRuntimeConfig(ctx, "另一配置", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateConversation(ctx, p.ID, "", "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateConversation(ctx, p.ID, "", "B")
	if err != nil {
		t.Fatal(err)
	}
	if a.ContextID == b.ContextID {
		t.Fatal("same config merged contexts")
	}
	local, err := s.GetLocalContext(ctx, a.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	if local.Policy.ApprovalMode != "semi" || local.Policy.Permissions.Unrestricted {
		t.Fatalf("new context has unexpected grants: %+v", local)
	}
	if err := s.SetExfilGrant(ctx, ExfilGrant{ContextID: a.ContextID, Destination: "https://example.com", Scopes: []string{ScopePersonal}}); err != nil {
		t.Fatal(err)
	}
	grants, err := s.ListExfilGrants(ctx, b.ContextID)
	if err != nil || len(grants) != 0 {
		t.Fatalf("grant leaked: %+v %v", grants, err)
	}
	reused, err := s.CreateConversation(ctx, q.ID, a.ID, "explicit reuse")
	if err != nil {
		t.Fatal(err)
	}
	if reused.ContextID != a.ContextID {
		t.Fatal("explicit reuse lost context")
	}
	run, snapshot, err := s.CreateRun(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectConversationConfig(ctx, a.ID, q.ID); err != nil {
		t.Fatal(err)
	}
	old, found, err := s.ProfileSnapshot(ctx, run.ID)
	if err != nil || !found || old.SystemPrompt != snapshot.SystemPrompt {
		t.Fatal("in-flight snapshot changed")
	}
	if err := s.SetRunStatus(ctx, run.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	next, resolved, err := s.CreateRun(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.ConfigID != q.ID || resolved.ContextID != a.ContextID {
		t.Fatal("selection changed identity or used old config")
	}
	// Shared-context conversations retain their individual config choice.
	if _, err := s.SelectConversationConfig(ctx, reused.ID, DefaultConfigID); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetConversation(ctx, a.ID)
	if current.ConfigID != q.ID {
		t.Fatal("selection changed sibling conversation")
	}
}

func TestComponentProjectionAndRevisionHistory(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.CreateRuntimeConfig(ctx, "默认", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	p.Data.Execution.Main.SystemPrompt = "main override"
	p.Data.Execution.Main.RestrictTools = true
	p.Data.Execution.Main.AllowedTools = []string{}
	p.Data.Execution.Worker.SystemPrompt = "worker override"
	p, err = s.UpdateRuntimeConfig(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	main, err := p.Data.ResolveComponent("main")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := p.Data.ResolveComponent("worker")
	if err != nil {
		t.Fatal(err)
	}
	if main.SystemPrompt != "main override" || main.AllowsTool("local__current_time") || worker.SystemPrompt != "worker override" || !worker.AllowsTool("local__current_time") {
		t.Fatal("component projection broadened tools or changed sibling")
	}
	p.Data.Execution.Main.AllowedTools = []string{"external__execute"}
	if _, err := s.UpdateRuntimeConfig(ctx, p); err == nil {
		t.Fatal("component added an unavailable tool")
	}
	p.Data.Execution.Main.AllowedTools = nil
	p.Data.Execution.Workflow = "planned"
	if _, err := s.UpdateRuntimeConfig(ctx, p); err == nil {
		t.Fatal("unimplemented workflow accepted")
	}
	p.Data.Execution.Workflow = "direct"
	stale := p
	stale.Revision = 1
	if _, err := s.UpdateRuntimeConfig(ctx, stale); err == nil {
		t.Fatal("stale update overwrote new revision")
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM runtime_config_revisions WHERE config_id=?`, p.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("revision history missing: %d %v", n, err)
	}
}

// The auto-created default config must survive the real persistence and
// conversation creation path.
func TestDefaultConfigIsCreatedAndSelected(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	configs, err := s.ListRuntimeConfigs(ctx)
	if err != nil || len(configs) != 1 || configs[0].ID != DefaultConfigID {
		t.Fatalf("default config missing: %+v %v", configs, err)
	}
	conversation, err := s.CreateConversation(ctx, DefaultConfigID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if conversation.ConfigID != DefaultConfigID {
		t.Fatal("conversation did not fall back to the default config")
	}
	another, err := s.CreateRuntimeConfig(ctx, "第二配置", assistantData())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateConversation(ctx, another.ID, "", ""); err != nil {
		t.Fatal(err)
	}
}
