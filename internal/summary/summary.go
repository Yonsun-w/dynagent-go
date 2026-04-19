package summary

import (
	"context"

	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/state"
)

type Generator struct{}

func New() *Generator {
	return &Generator{}
}

func (g *Generator) Build(ctx context.Context, st *state.State, record persistence.TaskRecord, terminationReason string) map[string]any {
	_ = ctx
	sequence := make([]string, 0, len(record.Steps))
	for _, step := range record.Steps {
		sequence = append(sequence, step.NodeID)
	}
	finalConclusion := ""
	if output, ok := st.NodeOutputs["finalize"]; ok {
		if value, ok := output["final_text"].(string); ok {
			finalConclusion = value
		}
	}
	return map[string]any{
		"task_id":            st.Task.ID,
		"status":             st.Task.Status,
		"keywords":           st.UserInput.Keywords,
		"node_sequence":      sequence,
		"final_conclusion":   finalConclusion,
		"termination_reason": terminationReason,
		"reasoning_log":      st.DecisionLog,
		"key_results":        st.NodeOutputs,
	}
}
