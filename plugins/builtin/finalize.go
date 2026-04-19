package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type finalizeNode struct{}

func (finalizeNode) Meta() node.Meta {
	return node.Meta{
		ID:          "finalize",
		Version:     "v1",
		Description: "Produce a final answer from accumulated working memory.",
		Labels:      []string{"builtin", "terminal"},
		OutputSchema: node.Schema{Required: []string{"final_text"}},
	}
}

func (finalizeNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	snapshot := st.Snapshot()
	if _, ok := snapshot.WorkingMemory["transformed_text"]; !ok && strings.TrimSpace(snapshot.UserInput.Text) == "" {
		return node.CheckResult{Allowed: false, Reason: "nothing to finalize"}
	}
	return node.CheckResult{Allowed: true}
}

func (finalizeNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	snapshot := st.Snapshot()
	text := snapshot.UserInput.Text
	if transformed, ok := snapshot.WorkingMemory["transformed_text"].(string); ok && transformed != "" {
		text = transformed
	}
	intent, _ := snapshot.WorkingMemory["intent"].(string)
	finalText := fmt.Sprintf("intent=%s result=%s", intent, text)
	return node.Result{
		Success: true,
		Output: map[string]any{"final_text": finalText},
		Patch: state.Patch{
			NodeOutputs: map[string]map[string]any{
				"finalize": {"final_text": finalText},
			},
			WorkingMemory: map[string]any{
				"final_text": finalText,
			},
		},
	}
}
