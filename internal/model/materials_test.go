package model

import (
	"encoding/json"
	"icetudio/internal/materials"
	"strings"
	"testing"
)

func TestAttachmentWireFormats(t *testing.T) {
	messages := []Message{{Role: "user", Content: "分析材料", Files: []materials.File{{Name: "note.txt", MediaType: "text/plain", Text: "unique-document-text"}, {Name: "image.png", MediaType: "image/png", Data: "aW1hZ2U="}, {Name: "paper.pdf", MediaType: "application/pdf", Data: "cGRm"}}}}
	cases := map[string]func() (any, error){
		"openai":    func() (any, error) { return openaiMessages(messages) },
		"responses": func() (any, error) { return responseSearchParams(openaiSource("http://localhost"), messages, nil, 1) },
		"anthropic": func() (any, error) { return anthropicRequests(messages, nil) },
		"gemini":    func() (any, error) { return geminiContents(messages) },
		"interactions": func() (any, error) {
			return geminiSearchParams(geminiSource("http://localhost"), messages, nil, false, false)
		},
		"compat": func() (any, error) { return compatMessages(messages), nil },
	}
	for name, convert := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := convert()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"unique-document-text", "aW1hZ2U=", "cGRm"} {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("lost %s: %s", want, raw)
				}
			}
			if strings.Contains(string(raw), `"files":`) {
				t.Fatalf("internal attachment shape leaked: %s", raw)
			}
		})
	}
}
