package weatherdemo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/admin/ai_project/internal/ai"
	"github.com/admin/ai_project/internal/app"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
)

const (
	nodeResolveLocation = "resolve_user_location"
	nodeQueryWeather    = "query_weather"
	nodeFinalizeWeather = "finalize_weather_answer"
)

type Result struct {
	TaskID string         `json:"task_id"`
	Output map[string]any `json:"output"`
}

func Run(ctx context.Context, cfg config.Config, prompt string, verbose bool) (Result, error) {
	if err := ai.ValidateProviderRuntime(cfg.AI.Primary); err != nil {
		return Result{}, fmt.Errorf("primary provider preflight failed: %w", err)
	}
	if err := ai.ValidateProviderRuntime(cfg.AI.Fallback); err != nil {
		return Result{}, fmt.Errorf("fallback provider preflight failed: %w", err)
	}

	appInstance, err := app.New(ctx, cfg)
	if err != nil {
		return Result{}, fmt.Errorf("initialize app: %w", err)
	}
	defer func() {
		_ = appInstance.Close(context.Background())
	}()

	for _, n := range []node.Node{
		resolveUserLocationNode{},
		queryWeatherNode{},
		finalizeWeatherAnswerNode{},
	} {
		if err := appInstance.Registry.RegisterBuiltin(n); err != nil {
			return Result{}, fmt.Errorf("register weather node %q: %w", n.Meta().ID, err)
		}
	}

	taskID := fmt.Sprintf("weather-demo-%d", time.Now().UnixNano())
	st, err := state.New(taskID, platform.NewTraceID(), state.UserInput{
		Text:     strings.TrimSpace(prompt),
		Keywords: []string{"weather", "demo", "tool_call"},
		Ext: map[string]any{
			"scene": "weather_demo",
		},
	}, map[string]string{"demo": "weather"})
	if err != nil {
		return Result{}, fmt.Errorf("initialize state: %w", err)
	}

	summaryPayload, err := appInstance.Engine.Run(ctx, st)
	if err != nil {
		return Result{}, fmt.Errorf("run engine: %w", err)
	}

	record, err := appInstance.Store.GetTask(ctx, taskID)
	if err != nil {
		return Result{}, fmt.Errorf("load task record: %w", err)
	}

	readonly, err := record.State.ReadOnly()
	if err != nil {
		return Result{}, fmt.Errorf("build readonly state: %w", err)
	}

	metas := appInstance.Registry.List()
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].ID < metas[j].ID
	})
	candidates := buildCandidateNodes(metas)
	routingContext := ai.RoutingContext{
		TaskID:           taskID,
		UserInput:        prompt,
		Keywords:         record.State.UserInput.Keywords,
		State:            readonly,
		CandidateNodes:   candidates,
		RecommendedNodes: []string{nodeResolveLocation, nodeQueryWeather, nodeFinalizeWeather},
		PlanningEnabled:  cfg.AI.RoutingMode == "route_and_plan",
	}
	req := ai.Request{Context: routingContext}

	primaryRegistration, err := ai.BuildProviderRegistrationPayload(platform.ContextWithTraceID(ctx, record.State.Trace.TraceID), cfg.AI.Primary, req)
	if err != nil {
		return Result{}, fmt.Errorf("build primary provider registration payload: %w", err)
	}
	fallbackRegistration, err := ai.BuildProviderRegistrationPayload(platform.ContextWithTraceID(ctx, record.State.Trace.TraceID), cfg.AI.Fallback, req)
	if err != nil {
		return Result{}, fmt.Errorf("build fallback provider registration payload: %w", err)
	}

	output := map[string]any{
		"task_id": taskID,
		"provider_info": map[string]any{
			"primary":  ai.DescribeProvider(cfg.AI.Primary),
			"fallback": ai.DescribeProvider(cfg.AI.Fallback),
		},
		"task_input": map[string]any{
			"prompt":       prompt,
			"routing_mode": cfg.AI.RoutingMode,
			"max_steps":    cfg.Execution.MaxSteps,
			"task_timeout": cfg.Execution.TaskTimeout.String(),
		},
		"registered_nodes": buildRegisteredNodes(metas),
		"function_contracts": map[string]any{
			"definitions":  ai.BuildFunctionDefs(routingContext),
			"openai_tools": ai.BuildOpenAITools(routingContext),
		},
		"llm_registration": map[string]any{
			"primary": map[string]any{
				"provider":        ai.DescribeProvider(cfg.AI.Primary),
				"request_payload": primaryRegistration,
			},
			"fallback": map[string]any{
				"provider":        ai.DescribeProvider(cfg.AI.Fallback),
				"request_payload": fallbackRegistration,
			},
		},
		"decision_trace": record.State.DecisionLog,
		"node_outputs":   record.State.NodeOutputs,
		"final_summary":  summaryPayload,
		"how_it_works": []string{
			"AI gateway sends the routing context to the configured provider by function calling.",
			"LLM selects the next node identifier instead of executing business logic directly.",
			"Runtime validates admission rules, runs the node in sandbox, and merges only the returned patch.",
		},
	}

	if verbose {
		stateMap, _ := readonly.ToMap()
		output["routing_context"] = buildRoutingContextPayload(routingContext)
		output["anthropic_tools"] = ai.BuildAnthropicTools(routingContext)
		output["runtime_state"] = stateMap
		output["design_notes"] = map[string]any{
			"function_layer": "Functions are LLM-facing routing contracts with fixed JSON schema.",
			"node_layer":     "Nodes are runtime execution units that only read ReadOnlyState and emit Patch.",
			"weather_tools":  "resolve_user_location and query_weather simulate external tools but still run as framework nodes.",
		}
	}

	return Result{
		TaskID: taskID,
		Output: output,
	}, nil
}

func Marshal(result Result) ([]byte, error) {
	return json.MarshalIndent(result.Output, "", "  ")
}

type resolveUserLocationNode struct{}

func (resolveUserLocationNode) Meta() node.Meta {
	return node.Meta{
		ID:          nodeResolveLocation,
		Version:     "v1",
		Description: "Tool node: resolve the user's current location before weather lookup.",
		Labels:      []string{"weather", "tool", "location"},
		InputSchema: node.Schema{
			Required: []string{"user_input.text"},
		},
		OutputSchema: node.Schema{
			Required: []string{"location"},
		},
	}
}

func (resolveUserLocationNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	snapshot := st.Snapshot()
	if strings.TrimSpace(snapshot.UserInput.Text) == "" {
		return node.CheckResult{Allowed: false, Reason: "user input text is empty"}
	}
	if _, ok := snapshot.WorkingMemory["location"]; ok {
		return node.CheckResult{Allowed: false, Reason: "location already exists"}
	}
	return node.CheckResult{Allowed: true}
}

func (resolveUserLocationNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	_ = st

	// 这里模拟“定位服务”工具调用结果。
	location := "上海市浦东新区"
	return node.Result{
		Success: true,
		Output: map[string]any{
			"location": location,
		},
		Patch: state.Patch{
			WorkingMemory: map[string]any{
				"location": location,
			},
			NodeOutputs: map[string]map[string]any{
				nodeResolveLocation: {"location": location},
			},
		},
	}
}

type queryWeatherNode struct{}

func (queryWeatherNode) Meta() node.Meta {
	return node.Meta{
		ID:          nodeQueryWeather,
		Version:     "v1",
		Description: "Tool node: query weather data by location.",
		Labels:      []string{"weather", "tool"},
		InputSchema: node.Schema{
			Required: []string{"working_memory.location"},
		},
		OutputSchema: node.Schema{
			Required: []string{"weather"},
		},
	}
}

func (queryWeatherNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	snapshot := st.Snapshot()
	if strings.TrimSpace(fmt.Sprint(snapshot.WorkingMemory["location"])) == "" {
		return node.CheckResult{Allowed: false, Reason: "location is empty"}
	}
	if _, ok := snapshot.WorkingMemory["weather"]; ok {
		return node.CheckResult{Allowed: false, Reason: "weather already exists"}
	}
	return node.CheckResult{Allowed: true}
}

func (queryWeatherNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx

	// 这里模拟“天气服务”工具调用结果。接入真实天气 API 时，只需要替换这个节点内部逻辑。
	location := fmt.Sprint(st.Snapshot().WorkingMemory["location"])
	weather := "多云，24°C，东南风 3 级"
	return node.Result{
		Success: true,
		Output: map[string]any{
			"location": location,
			"weather":  weather,
		},
		Patch: state.Patch{
			WorkingMemory: map[string]any{
				"weather": weather,
			},
			NodeOutputs: map[string]map[string]any{
				nodeQueryWeather: {
					"location": location,
					"weather":  weather,
				},
			},
		},
	}
}

type finalizeWeatherAnswerNode struct{}

func (finalizeWeatherAnswerNode) Meta() node.Meta {
	return node.Meta{
		ID:          nodeFinalizeWeather,
		Version:     "v1",
		Description: "Terminal node: generate the final answer for the user from location and weather.",
		Labels:      []string{"weather", "terminal"},
		InputSchema: node.Schema{
			Required: []string{"working_memory.location", "working_memory.weather"},
		},
		OutputSchema: node.Schema{
			Required: []string{"final_text"},
		},
	}
}

func (finalizeWeatherAnswerNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	snapshot := st.Snapshot()
	if strings.TrimSpace(fmt.Sprint(snapshot.WorkingMemory["location"])) == "" {
		return node.CheckResult{Allowed: false, Reason: "location is empty"}
	}
	if strings.TrimSpace(fmt.Sprint(snapshot.WorkingMemory["weather"])) == "" {
		return node.CheckResult{Allowed: false, Reason: "weather is empty"}
	}
	if _, ok := snapshot.WorkingMemory["final_text"]; ok {
		return node.CheckResult{Allowed: false, Reason: "final_text already exists"}
	}
	return node.CheckResult{Allowed: true}
}

func (finalizeWeatherAnswerNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	snapshot := st.Snapshot()
	location := fmt.Sprint(snapshot.WorkingMemory["location"])
	weather := fmt.Sprint(snapshot.WorkingMemory["weather"])
	finalText := fmt.Sprintf("你当前在%s，天气%s。建议带伞，穿一件轻薄外套。", location, weather)
	return node.Result{
		Success: true,
		Output: map[string]any{
			"final_text": finalText,
		},
		Patch: state.Patch{
			WorkingMemory: map[string]any{
				"final_text": finalText,
			},
			NodeOutputs: map[string]map[string]any{
				nodeFinalizeWeather: {"final_text": finalText},
			},
		},
	}
}

func buildCandidateNodes(metas []node.Meta) []ai.CandidateNode {
	out := make([]ai.CandidateNode, 0, len(metas))
	for _, meta := range metas {
		out = append(out, ai.CandidateNode{
			ID:          meta.ID,
			Description: meta.Description,
			Labels:      append([]string(nil), meta.Labels...),
		})
	}
	return out
}

func buildRegisteredNodes(metas []node.Meta) []map[string]any {
	out := make([]map[string]any, 0, len(metas))
	for _, meta := range metas {
		out = append(out, map[string]any{
			"id":            meta.ID,
			"description":   meta.Description,
			"labels":        meta.Labels,
			"input_schema":  meta.InputSchema,
			"output_schema": meta.OutputSchema,
		})
	}
	return out
}

func buildRoutingContextPayload(ctx ai.RoutingContext) map[string]any {
	snapshot, _ := ctx.State.ToMap()
	return map[string]any{
		"task_id":               ctx.TaskID,
		"user_input":            ctx.UserInput,
		"keywords":              ctx.Keywords,
		"candidate_nodes":       ctx.CandidateNodes,
		"recommended_nodes":     ctx.RecommendedNodes,
		"planning_enabled":      ctx.PlanningEnabled,
		"last_rejection_reason": ctx.LastRejectionReason,
		"state":                 snapshot,
	}
}
