package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type panicNode struct{}

func (panicNode) Meta() node.Meta { return node.Meta{ID: "panic"} }
func (panicNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	_ = st
	return node.CheckResult{Allowed: true}
}
func (panicNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	_ = st
	panic("boom")
}

func TestExecutorRecoversPanic(t *testing.T) {
	executor := New(1, 100*time.Millisecond)
	st, err := state.New("task-1", "trace-1", state.UserInput{Text: "hello"}, nil)
	if err != nil {
		t.Fatalf("state.New() error = %v", err)
	}
	readonly, err := st.ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly() error = %v", err)
	}
	result, err := executor.Execute(context.Background(), panicNode{}, readonly)
	if err == nil {
		t.Fatalf("expected error from panic recovery")
	}
	if result.Success {
		t.Fatalf("expected failed result")
	}
}
