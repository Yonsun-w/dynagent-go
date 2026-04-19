package builtin

import (
	"context"
	"strings"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type textTransformNode struct{}

func (textTransformNode) Meta() node.Meta {
	return node.Meta{
		ID:          "text_transform",
		Version:     "v1",
		Description: "Normalize and transform user text into a concise enriched form.",
		Labels:      []string{"builtin", "text"},
		InputSchema: node.Schema{Required: []string{"user_input.text"}},
		OutputSchema: node.Schema{Required: []string{"transformed_text"}},
	}
}

func (textTransformNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	if strings.TrimSpace(st.UserText()) == "" {
		return node.CheckResult{Allowed: false, Reason: "user input text is empty"}
	}
	return node.CheckResult{Allowed: true}
}

func (textTransformNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	text := strings.TrimSpace(st.UserText())
	transformed := strings.ToUpper(text)
	return node.Result{
		Success: true,
		Output: map[string]any{"transformed_text": transformed},
		Patch: state.Patch{
			WorkingMemory: map[string]any{
				"transformed_text": transformed,
			},
			NodeOutputs: map[string]map[string]any{
				"text_transform": {"transformed_text": transformed},
			},
		},
	}
}
