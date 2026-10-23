package storage

import (
	"context"

	"testing"
)

func TestProfilePublicationIsAtomic(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	p, err := s.GetRuntimeConfig(ctx, DefaultConfigID)
	if err != nil {
		t.Fatal(err)
	}
	// Reject model changes to verify the entire publication rolls back.
	if _, err := s.db.Exec(`CREATE TRIGGER model_unchanged BEFORE UPDATE ON runtime_configs WHEN json_extract(NEW.config,'$.model.name') != json_extract(OLD.config,'$.model.name') BEGIN SELECT RAISE(ABORT,'model change rejected'); END`); err != nil {
		t.Fatal(err)
	}
	p.Data.SystemPrompt = "new prompt"
	p.Data.Persona = "calm"
	p, err = s.UpdateRuntimeConfig(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Data.Persona != "calm" || p.Data.SystemPrompt != "new prompt" {
		t.Fatalf("lost components: %+v", p)
	}
	before := p
	p.Data.Model.Name = "new model"
	p.Data.SystemPrompt = "must roll back"
	if _, err = s.UpdateRuntimeConfig(ctx, p); err == nil {
		t.Fatal("component failure must abort publication")
	}
	after, err := s.GetRuntimeConfig(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Data.SystemPrompt != before.Data.SystemPrompt {
		t.Fatal("partial publication")
	}
}
