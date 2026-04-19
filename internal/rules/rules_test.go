package rules

import (
	"context"
	"testing"

	"github.com/admin/ai_project/internal/state"
)

func TestEvaluatorSupportsDotStyleStatePath(t *testing.T) {
	evaluator, err := NewEvaluator()
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	st, err := state.New("task-1", "trace-1", state.UserInput{Text: "hello"}, nil)
	if err != nil {
		t.Fatalf("state.New() error = %v", err)
	}
	readonly, err := st.ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly() error = %v", err)
	}
	result := evaluator.Evaluate(context.Background(), RuleSet{
		Operator: "and",
		Rules: []Rule{
			{Expression: "state.user_input.text != ''", Reason: "text is required"},
		},
	}, readonly)
	if !result.Allowed {
		t.Fatalf("expected allowed result, got %#v", result)
	}
	result = evaluator.Evaluate(context.Background(), RuleSet{
		Operator: "and",
		Rules: []Rule{
			{Expression: "state.user_input.text == ''", Reason: "text must be empty"},
		},
	}, readonly)
	if result.Allowed {
		t.Fatalf("expected reject result, got %#v", result)
	}
}
