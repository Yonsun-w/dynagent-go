package ai

import (
	"context"
	"testing"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
)

func TestParseOpenAIFunctionCall_RejectsDirectJSON(t *testing.T) {
	t.Parallel()

	_, err := parseOpenAIFunctionCall([]byte(`{"next_node":"finalize","reasoning":"done","data":{}}`))
	if err == nil {
		t.Fatalf("expected direct json payload to be rejected")
	}
}

func TestParseOpenAIFunctionCall_ChoicesToolCall(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"choices": [{
			"message": {
				"tool_calls": [{
					"id": "call-1",
					"type": "function",
					"function": {
						"name": "route_next_node",
						"arguments": "{\"next_node\":\"weather_lookup\",\"reasoning\":\"tool call\",\"data\":{\"city\":\"shanghai\"}}"
					}
				}]
			}
		}]
	}`)

	call, err := parseOpenAIFunctionCall(raw)
	if err != nil {
		t.Fatalf("parseOpenAIFunctionCall returned error: %v", err)
	}
	if call.Name != RouteFunctionName {
		t.Fatalf("unexpected function name: %s", call.Name)
	}
	if got := call.Arguments["next_node"]; got != "weather_lookup" {
		t.Fatalf("unexpected next node argument: %v", got)
	}
}

func TestParseAnthropicFunctionCall_ToolUse(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"content": [{
			"type": "tool_use",
			"id": "toolu_1",
			"name": "propose_dag",
			"input": {
				"goal": "weather task",
				"nodes": ["resolve_user_location", "query_weather"],
				"edges": [{"from":"resolve_user_location","to":"query_weather"}],
				"reasoning": "plan first",
				"data": {}
			}
		}]
	}`)

	call, err := parseAnthropicFunctionCall(raw)
	if err != nil {
		t.Fatalf("parseAnthropicFunctionCall returned error: %v", err)
	}
	if call.Name != PlanFunctionName {
		t.Fatalf("unexpected function name: %s", call.Name)
	}
}

func TestBuildOpenAIRequest_UsesFunctionCallingContract(t *testing.T) {
	t.Parallel()

	ctx := platform.ContextWithTraceID(context.Background(), "trace-test")
	payload, err := buildOpenAIRequest(ctx, modelConfigForTest(), Request{
		Context: RoutingContext{
			TaskID:           "task-route",
			UserInput:        "帮我查天气",
			Keywords:         []string{"天气"},
			State:            mustReadOnlyState(t),
			CandidateNodes:   []CandidateNode{{ID: "resolve_user_location"}, {ID: "query_weather"}},
			RecommendedNodes: []string{"resolve_user_location"},
			PlanningEnabled:  true,
		},
	})
	if err != nil {
		t.Fatalf("buildOpenAIRequest returned error: %v", err)
	}

	if payload["tool_choice"] != "required" {
		t.Fatalf("expected tool_choice=required, got %#v", payload["tool_choice"])
	}
	tools, ok := payload["tools"].([]map[string]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected two function tools, got %#v", payload["tools"])
	}
}

func TestNormalizeFunctionCall_RouteAndPlan(t *testing.T) {
	t.Parallel()

	req := Request{
		Context: RoutingContext{
			State:           mustReadOnlyState(t),
			CandidateNodes:  []CandidateNode{{ID: "resolve_user_location"}, {ID: "query_weather"}},
			PlanningEnabled: true,
		},
	}

	route, err := normalizeFunctionCall(req, FunctionCall{
		Name: RouteFunctionName,
		Arguments: map[string]any{
			"next_node": "query_weather",
			"reasoning": "route",
			"data":      map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("normalizeFunctionCall(route) error = %v", err)
	}
	if route.Type != DecisionTypeRoute || route.Route == nil {
		t.Fatalf("expected route decision, got %#v", route)
	}

	plan, err := normalizeFunctionCall(req, FunctionCall{
		Name: PlanFunctionName,
		Arguments: map[string]any{
			"goal":      "weather",
			"nodes":     []any{"resolve_user_location", "query_weather"},
			"edges":     []any{map[string]any{"from": "resolve_user_location", "to": "query_weather"}},
			"reasoning": "plan",
			"data":      map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("normalizeFunctionCall(plan) error = %v", err)
	}
	if plan.Type != DecisionTypePlan || plan.Plan == nil {
		t.Fatalf("expected plan decision, got %#v", plan)
	}
}

func modelConfigForTest() config.ModelConfig {
	return config.ModelConfig{
		Provider: "mock_function",
		Model:    "mock-function-router",
	}
}

func mustReadOnlyState(t *testing.T) *state.ReadOnlyState {
	t.Helper()

	st, err := state.New("task-ai", "trace-ai", state.UserInput{
		Text:     "route task",
		Keywords: []string{"route"},
		Ext:      map[string]any{},
	}, map[string]string{"suite": "ai"})
	if err != nil {
		t.Fatalf("state.New returned error: %v", err)
	}
	readonly, err := st.ReadOnly()
	if err != nil {
		t.Fatalf("ReadOnly returned error: %v", err)
	}
	return readonly
}
