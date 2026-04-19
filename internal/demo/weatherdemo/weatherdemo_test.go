package weatherdemo

import (
	"context"
	"testing"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/state"
)

func TestRun_CompletesWeatherWorkflowWithMockProvider(t *testing.T) {
	t.Parallel()

	cfg := config.Config{}
	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		t.Fatalf("ApplyDefaultsAndValidate() error = %v", err)
	}
	cfg.AI.RoutingMode = "route_and_plan"

	result, err := Run(context.Background(), cfg, "帮我查一下我当前位置的天气，并告诉我要不要带伞", false)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.TaskID == "" {
		t.Fatalf("expected non-empty task id")
	}

	summary, ok := result.Output["final_summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected final_summary payload")
	}
	if summary["final_conclusion"] == "" {
		t.Fatalf("expected final_conclusion in summary payload")
	}

	trace, ok := result.Output["decision_trace"].([]state.DecisionRecord)
	if !ok {
		t.Fatalf("expected typed decision trace")
	}
	if len(trace) == 0 {
		t.Fatalf("expected non-empty decision trace")
	}
}
