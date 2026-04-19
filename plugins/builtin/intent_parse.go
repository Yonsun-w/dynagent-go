package builtin

import (
	"context"
	"strings"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type intentParseNode struct{}

func (intentParseNode) Meta() node.Meta {
	return node.Meta{
		ID:          "intent_parse",
		Version:     "v1",
		Description: "Parse the raw user input into a normalized intent and keywords.",
		Labels:      []string{"builtin", "nlp"},
		InputSchema: node.Schema{Required: []string{"user_input.text"}},
		OutputSchema: node.Schema{Required: []string{"intent", "keywords"}},
	}
}

func (intentParseNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	if strings.TrimSpace(st.UserText()) == "" {
		return node.CheckResult{Allowed: false, Reason: "user input text is empty"}
	}
	return node.CheckResult{Allowed: true}
}

func (intentParseNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	text := strings.TrimSpace(st.UserText())
	intent := "general"
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "translate"):
		intent = "translate"
	case strings.Contains(lower, "summarize"):
		intent = "summarize"
	case strings.Contains(lower, "http"), strings.Contains(lower, "api"):
		intent = "external_call"
	}
	keywords := extractKeywords(lower)
	return node.Result{
		Success: true,
		Output: map[string]any{
			"intent":   intent,
			"keywords": keywords,
		},
		Patch: state.Patch{
			WorkingMemory: map[string]any{
				"intent": intent,
			},
			NodeOutputs: map[string]map[string]any{
				"intent_parse": {
					"intent":   intent,
					"keywords": keywords,
				},
			},
		},
	}
}

func extractKeywords(text string) []string {
	raw := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == ';' || r == ':' || r == '\n' || r == '\t'
	})
	set := map[string]struct{}{}
	var out []string
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if len(item) < 4 {
			continue
		}
		if _, ok := set[item]; ok {
			continue
		}
		set[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
