package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"icetudio/internal/config"
	"icetudio/internal/mcptools"
	"icetudio/internal/model"
	"icetudio/internal/project"
	"icetudio/internal/storage"
)

func materialTestTool() model.Tool {
	d := model.Tool{Type: "function"}
	d.Function.Name = "music__analyze"
	d.Function.Description = "analyze supplied music material"
	d.Function.Parameters = json.RawMessage(`{"type":"object","properties":{"score":{"type":"string"},"purpose":{"type":"string"}},"required":["score"],"additionalProperties":false}`)
	return d
}

func TestProjectMaterialPreparationBounds(t *testing.T) {
	ctx, ids, tools := scopeFixture(t, nil)
	run, profile, err := tools.svc.store.CreateRunWithOptions(ctx, tools.sessionID, storage.CreateRunOptions{ProjectScope: fmt.Sprintf(`{"projectId":%q,"baseRevision":0,"accessMode":"write","clips":[%q]}`, ids.Project, ids.C1)})
	if err != nil {
		t.Fatal(err)
	}
	tools.runID = run.ID
	tools.profile = profile
	tools.inner = &scriptTools{defs: []model.Tool{materialTestTool()}}
	tools.svc.endpoints = &fakeEndpointSource{infos: []mcptools.EndpointInfo{{Name: "music", Command: "helper", BinarySHA256: "binary", SchemaSHA256: "schema"}}}
	for _, raw := range []string{
		fmt.Sprintf(`{"query":{"projectId":%q},"tool":"music__analyze","field":"score","arguments":{}}`, ids.Project),
		fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"music__analyze","field":"score","arguments":{}}`, ids.Project, ids.C2),
		fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q,"unknown":true},"tool":"music__analyze","field":"score","arguments":{}}`, ids.Project, ids.C1),
		fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"local__exec","field":"score","arguments":{}}`, ids.Project, ids.C1),
		fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"music__analyze","field":"score","arguments":{"score":"overwrite"}}`, ids.Project, ids.C1),
		fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"music__analyze","field":"missing","arguments":{}}`, ids.Project, ids.C1),
	} {
		if _, err := tools.prepareProjectMaterial(ctx, raw); err == nil {
			t.Fatal("invalid preparation accepted", raw)
		}
	}
	tools.profile.RestrictTools = true
	tools.profile.AllowedTools = nil
	if _, err := tools.prepareProjectMaterial(ctx, `{}`); err == nil {
		t.Fatal("empty capability ceiling bypassed")
	}
}

func TestProjectMaterialEngineFrozenApprovalAndSharedUndo(t *testing.T) {
	for _, scenario := range []string{"approved", "search", "denied", "identity_changed", "schema_changed", "deleted", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, ids, fixture := scopeFixture(t, nil)
			s := fixture.svc
			accepted := scenario == "approved" || scenario == "search"
			var sent atomic.Int32
			endpoint := &fakeEndpointSource{infos: []mcptools.EndpointInfo{{Name: "music", Command: "/usr/bin/music-helper", BinarySHA256: "binary-v1", SchemaSHA256: "schema-v1"}}}
			tools := &scriptTools{defs: []model.Tool{materialTestTool()}, respond: func(name, args string) (string, error) {
				sent.Add(1)
				var data map[string]string
				if name != "music__analyze" || json.Unmarshal([]byte(args), &data) != nil {
					return "", errors.New("bad target")
				}
				if !strings.Contains(data["score"], `"pitch":60`) || strings.Contains(data["score"], ids.C2) || strings.Contains(data["score"], "NEW_PRIVATE_TITLE") {
					return "", errors.New("material changed or exceeded scope")
				}
				return `{"advice":"increase selected velocity","url":"https://example.org/music"}`, nil
			}}
			s.tools = tools
			s.endpoints = endpoint
			if scenario == "search" {
				s.config.MCPServers = []config.MCPServer{{Name: "music", Command: "mock", WebSearchTools: []string{"analyze"}}}
				if _, err := s.SetConversationOptions(fixture.sessionID, storage.ConversationOptions{WebSearch: true}); err != nil {
					t.Fatal(err)
				}
			}
			data := storage.DefaultRuntimeConfig()
			data.ContextBudget = 32768
			if _, err := s.configureDefault(data); err != nil {
				t.Fatal(err)
			}
			var prepared preparedProjectMaterial
			call := func(id, name, raw string) (model.ChatResponse, error) {
				return model.ChatResponse{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{extToolCall(id, name, raw)}}}, nil
			}
			s.chat = &scriptModel{steps: []func([]model.Message) (model.ChatResponse, error){
				func(_ []model.Message) (model.ChatResponse, error) {
					return call("prepare", "project__prepare_material", fmt.Sprintf(`{"query":{"projectId":%q,"clipId":%q},"tool":"music__analyze","field":"score","arguments":{"purpose":"voicing"}}`, ids.Project, ids.C1))
				},
				func(messages []model.Message) (model.ChatResponse, error) {
					if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &prepared); err != nil {
						return model.ChatResponse{}, err
					}
					return call("send", "local__send_material", fmt.Sprintf(`{"preparationId":%q}`, prepared.ID))
				},
				func(messages []model.Message) (model.ChatResponse, error) {
					last := messages[len(messages)-1].Content
					if !accepted {
						if sent.Load() != 0 || !strings.Contains(last, "错误") && !strings.Contains(last, "用户拒绝") {
							return model.ChatResponse{}, fmt.Errorf("not rejected: %s", last)
						}
						return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "未发送"}}, nil
					}
					if !strings.Contains(last, "external_mcp") {
						return model.ChatResponse{}, errors.New("missing material source")
					}
					return call("fresh-query", "project__query", fmt.Sprintf(`{"projectId":%q,"clipId":%q}`, ids.Project, ids.C1))
				},
				func(messages []model.Message) (model.ChatResponse, error) {
					var q struct {
						Revision int64         `json:"revision"`
						State    project.State `json:"state"`
					}
					if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &q); err != nil {
						return model.ChatResponse{}, err
					}
					note := q.State.Tracks[0].Clips[0].Notes[0].ID
					return call("edit-from-advice", "project__edit", editPayload(ids.Project, q.Revision, "advice-edit", fmt.Sprintf(`{"kind":"note.update","clipId":%q,"noteDeltas":[{"id":%q,"velocity":110}]}`, ids.C1, note)))
				},
				func(messages []model.Message) (model.ChatResponse, error) {
					if strings.Contains(messages[len(messages)-1].Content, "错误") {
						return model.ChatResponse{}, errors.New(messages[len(messages)-1].Content)
					}
					return model.ChatResponse{Message: model.Message{Role: "assistant", Content: "已通过原生事务修改"}}, nil
				},
			}}
			run, err := s.StartConversationTask(fixture.sessionID, "分析所选片段后改善力度", &ProjectScopeSelection{Clips: []string{ids.C1}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.CancelTask(run.ID); s.WaitIdle() }()
			pending := waitForProjectWrite(t, s, fixture.sessionID, "local__send_material")
			// A newly persisted pending row precedes Gate evaluation. Wait for
			// the actual user-approval suspension before changing live metadata.
			deadline := time.Now().Add(3 * time.Second)
			waiting := false
			for time.Now().Before(deadline) {
				s.muApprovals.Lock()
				_, waiting = s.waiters[pending.ID]
				s.muApprovals.Unlock()
				if waiting {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !waiting {
				t.Fatal("engine did not suspend for material approval")
			}
			conv, _ := s.GetConversation(fixture.sessionID)
			approvals, err := s.PendingApprovals(conv.ContextID)
			if err != nil {
				t.Fatal(err)
			}
			var view *storage.MaterialApproval
			for _, a := range approvals {
				if a.ID == pending.ID {
					view = a.Material
				}
			}
			if view == nil || !strings.Contains(view.Destination, "music-helper") || view.Revision != prepared.Revision || !strings.Contains(view.Arguments, `\"pitch\":60`) {
				t.Fatalf("missing concrete material approval: %+v", view)
			}
			if sent.Load() != 0 {
				t.Fatal("sent before approval")
			}
			pitch := 62
			before, _ := s.GetProject(ids.Project)
			clip := before.State.Tracks[0].Clips[0]
			note := clip.Notes[0]
			_, err = s.SubmitProjectOps(ids.Project, "user-while-pending", "gui", before.Revision, []project.Op{{Kind: "project.rename", Title: "NEW_PRIVATE_TITLE"}, {Kind: "note.update", ClipID: clip.ID, NoteDeltas: []project.NoteDelta{{ID: note.ID, Pitch: &pitch}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SetConversationProject(fixture.sessionID, ""); err != nil {
				t.Fatal(err)
			}
			if scenario == "identity_changed" {
				endpoint.infos[0].BinarySHA256 = "binary-v2"
			}
			if scenario == "schema_changed" {
				tools.defs[0].Function.Description = "changed"
			}
			if scenario == "deleted" {
				if err = s.DeleteProject(ids.Project); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "cancelled" {
				if err = s.CancelTask(run.ID); err != nil {
					t.Fatal(err)
				}
				s.WaitIdle()
				if sent.Load() != 0 {
					t.Fatal("cancelled send ran")
				}
				return
			}
			if scenario == "denied" {
				_, err = s.DenyToolCall(pending.ID, "不发送")
			} else {
				_, err = s.ApproveToolCall(pending.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if accepted {
				edit := waitForProjectWrite(t, s, fixture.sessionID, "project__edit")
				if _, err = s.ApproveToolCall(edit.ID); err != nil {
					t.Fatal(err)
				}
			}
			waitForRun(t, s, run.ID, "completed")
			if accepted {
				if scenario == "search" {
					records, e := s.ListConversationSearch(fixture.sessionID)
					if e != nil || len(records) != 1 || records[0].Provider != "music__analyze" || len(records[0].Report.Citations) != 1 {
						t.Fatal("material search citations missing", records, e)
					}
				}
				if sent.Load() != 1 {
					t.Fatal("missing/duplicate dispatch")
				}
				after, _ := s.GetProject(ids.Project)
				got := after.State.Tracks[0].Clips[0].Notes[0]
				if got.Pitch != 62 || got.Velocity != 110 {
					t.Fatal(got)
				}
				if _, err = s.UndoProject(ids.Project, "undo-advice", "gui", after.Revision); err != nil {
					t.Fatal(err)
				}
				restored, _ := s.GetProject(ids.Project)
				got = restored.State.Tracks[0].Clips[0].Notes[0]
				if restored.Title != "NEW_PRIVATE_TITLE" || got.Pitch != 62 || got.Velocity != note.Velocity {
					t.Fatal("undo lost intervening user edit", got)
				}
				if _, err = s.store.PreparedProjectMaterial(ctx, "other-run", prepared.ID); err == nil {
					t.Fatal("cross-run material reference")
				}
			} else if sent.Load() != 0 {
				t.Fatal("rejected material sent")
			}
		})
	}
}
