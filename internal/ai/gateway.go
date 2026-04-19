package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
)

const (
	RouteFunctionName = "route_next_node"
	PlanFunctionName  = "propose_dag"
	terminateNode     = "__terminate__"
)

type DecisionType string

const (
	DecisionTypeRoute DecisionType = "route"
	DecisionTypePlan  DecisionType = "plan"
)

type CandidateNode struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Labels      []string `json:"labels,omitempty"`
}

type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	JSONSchema  map[string]any `json:"json_schema"`
}

type FunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	CallID    string         `json:"call_id,omitempty"`
}

type RouteDecision struct {
	NextNode  string         `json:"next_node"`
	Reasoning string         `json:"reasoning"`
	Data      map[string]any `json:"data"`
}

type DAGEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type DAGPlan struct {
	Goal      string         `json:"goal"`
	Nodes     []string       `json:"nodes"`
	Edges     []DAGEdge      `json:"edges"`
	Reasoning string         `json:"reasoning"`
	Data      map[string]any `json:"data"`
}

type Result struct {
	Type         DecisionType   `json:"type"`
	FunctionCall FunctionCall   `json:"function_call"`
	Route        *RouteDecision `json:"route,omitempty"`
	Plan         *DAGPlan       `json:"plan,omitempty"`
}

type RoutingContext struct {
	TaskID              string               `json:"task_id"`
	UserInput           string               `json:"user_input"`
	Keywords            []string             `json:"keywords"`
	State               *state.ReadOnlyState `json:"state"`
	CandidateNodes      []CandidateNode      `json:"candidate_nodes"`
	RecommendedNodes    []string             `json:"recommended_nodes"`
	LastRejectionReason string               `json:"last_rejection_reason,omitempty"`
	PlanningEnabled     bool                 `json:"planning_enabled"`
}

type Request struct {
	Context RoutingContext `json:"context"`
}

type Provider interface {
	Name() string
	Invoke(ctx context.Context, cfg config.ModelConfig, req Request) (FunctionCall, int, error)
}

type Gateway struct {
	logger   *zap.Logger
	primary  config.ModelConfig
	fallback config.ModelConfig
	retry    config.RetryConfig
	provider map[string]Provider
	limiter  *rate.Limiter
	breaker  *gobreaker.CircuitBreaker[Result]
}

func NewGateway(cfg config.AIConfig, logger *zap.Logger) *Gateway {
	breaker := gobreaker.NewCircuitBreaker[Result](gobreaker.Settings{
		Name:        "ai_gateway",
		MaxRequests: cfg.CircuitBreaker.MaxRequests,
		Interval:    cfg.CircuitBreaker.Interval,
		Timeout:     cfg.CircuitBreaker.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < 5 {
				return false
			}
			return float64(counts.TotalFailures)/float64(counts.Requests) >= cfg.CircuitBreaker.FailureRatio
		},
	})

	return &Gateway{
		logger:   logger,
		primary:  cfg.Primary,
		fallback: cfg.Fallback,
		retry:    cfg.Retry,
		provider: map[string]Provider{
			"mock_function":     mockFunctionProvider{},
			"openai":            openAIProvider{kind: "openai"},
			"openai_compatible": openAIProvider{kind: "openai_compatible"},
			"anthropic":         anthropicProvider{},
		},
		limiter: rate.NewLimiter(rate.Limit(cfg.RateLimit.RequestsPerSecond), cfg.RateLimit.Burst),
		breaker: breaker,
	}
}

func (g *Gateway) Decide(ctx context.Context, req Request) (Result, error) {
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	if err := g.limiter.Wait(ctx); err != nil {
		return Result{}, fmt.Errorf("rate limit wait: %w", err)
	}

	return g.breaker.Execute(func() (Result, error) {
		result, _, err := g.tryModel(ctx, g.primary, req)
		if err == nil {
			return result, nil
		}
		g.logger.Warn("primary ai model failed, trying fallback", zap.String("provider", g.primary.Provider), zap.Error(err))
		result, _, fallbackErr := g.tryModel(ctx, g.fallback, req)
		if fallbackErr != nil {
			return Result{}, fmt.Errorf("fallback failed after primary error %v: %w", err, fallbackErr)
		}
		return result, nil
	})
}

func (g *Gateway) tryModel(ctx context.Context, cfg config.ModelConfig, req Request) (Result, int, error) {
	provider, ok := g.provider[strings.ToLower(strings.TrimSpace(cfg.Provider))]
	if !ok {
		return Result{}, 0, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}

	var lastErr error
	for attempt := 1; attempt <= g.retry.MaxAttempts; attempt++ {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		childCtx, cancel := context.WithTimeout(ctx, timeout)
		functionCall, status, err := provider.Invoke(childCtx, cfg, req)
		cancel()
		if err == nil {
			result, normalizeErr := normalizeFunctionCall(req, functionCall)
			return result, status, normalizeErr
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return Result{}, status, ctx.Err()
		case <-time.After(time.Duration(attempt) * g.retry.BaseDelay):
		}
	}

	return Result{}, 0, lastErr
}

func validateRequest(req Request) error {
	if req.Context.State == nil {
		return errors.New("routing context state must not be nil")
	}
	if len(req.Context.CandidateNodes) == 0 {
		return errors.New("routing context candidate_nodes must not be empty")
	}
	return nil
}

func normalizeFunctionCall(req Request, call FunctionCall) (Result, error) {
	call.Name = strings.TrimSpace(call.Name)
	if call.Name == "" {
		return Result{}, errors.New("function call name must not be empty")
	}
	if call.Arguments == nil {
		return Result{}, errors.New("function call arguments must not be nil")
	}

	switch call.Name {
	case RouteFunctionName:
		decision, err := normalizeRouteDecision(req, call.Arguments)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Type:         DecisionTypeRoute,
			FunctionCall: call,
			Route:        &decision,
		}, nil
	case PlanFunctionName:
		if !req.Context.PlanningEnabled {
			return Result{}, errors.New("planning function is disabled")
		}
		plan, err := normalizePlan(req, call.Arguments)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Type:         DecisionTypePlan,
			FunctionCall: call,
			Plan:         &plan,
		}, nil
	default:
		return Result{}, fmt.Errorf("unsupported function %q", call.Name)
	}
}

func normalizeRouteDecision(req Request, args map[string]any) (RouteDecision, error) {
	nextNode, _ := args["next_node"].(string)
	reasoning, _ := args["reasoning"].(string)
	data, _ := args["data"].(map[string]any)
	nextNode = strings.TrimSpace(nextNode)
	if nextNode == "" {
		return RouteDecision{}, errors.New("route_next_node.next_node must not be empty")
	}
	if nextNode != terminateNode && !candidateNodeExists(req.Context.CandidateNodes, nextNode) {
		return RouteDecision{}, fmt.Errorf("route_next_node.next_node %q is not in candidate nodes", nextNode)
	}
	if data == nil {
		data = map[string]any{}
	}
	return RouteDecision{
		NextNode:  nextNode,
		Reasoning: reasoning,
		Data:      data,
	}, nil
}

func normalizePlan(req Request, args map[string]any) (DAGPlan, error) {
	goal, _ := args["goal"].(string)
	reasoning, _ := args["reasoning"].(string)
	data, _ := args["data"].(map[string]any)
	nodes, err := stringSlice(args["nodes"])
	if err != nil {
		return DAGPlan{}, fmt.Errorf("propose_dag.nodes: %w", err)
	}
	if len(nodes) == 0 {
		return DAGPlan{}, errors.New("propose_dag.nodes must not be empty")
	}
	for _, nodeID := range nodes {
		if !candidateNodeExists(req.Context.CandidateNodes, nodeID) {
			return DAGPlan{}, fmt.Errorf("propose_dag node %q is not in candidate nodes", nodeID)
		}
	}
	edges, err := dagEdges(args["edges"])
	if err != nil {
		return DAGPlan{}, fmt.Errorf("propose_dag.edges: %w", err)
	}
	if data == nil {
		data = map[string]any{}
	}
	return DAGPlan{
		Goal:      goal,
		Nodes:     nodes,
		Edges:     edges,
		Reasoning: reasoning,
		Data:      data,
	}, nil
}

func stringSlice(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("must be an array")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, errors.New("must contain only strings")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("must not contain empty strings")
		}
		out = append(out, value)
	}
	return out, nil
}

func dagEdges(raw any) ([]DAGEdge, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("must be an array")
	}
	out := make([]DAGEdge, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("must contain only objects")
		}
		from, _ := entry["from"].(string)
		to, _ := entry["to"].(string)
		from = strings.TrimSpace(from)
		to = strings.TrimSpace(to)
		if from == "" || to == "" {
			return nil, errors.New("edge from/to must not be empty")
		}
		out = append(out, DAGEdge{From: from, To: to})
	}
	return out, nil
}

func candidateNodeExists(candidates []CandidateNode, nodeID string) bool {
	for _, candidate := range candidates {
		if candidate.ID == nodeID {
			return true
		}
	}
	return false
}

type mockFunctionProvider struct{}

func (mockFunctionProvider) Name() string { return "mock_function" }

func (mockFunctionProvider) Invoke(ctx context.Context, cfg config.ModelConfig, req Request) (FunctionCall, int, error) {
	_ = ctx
	_ = cfg

	snapshot := req.Context.State.Snapshot()
	if req.Context.PlanningEnabled {
		if _, ok := snapshot.Ext["latest_plan"]; !ok {
			nodes := mockPlanNodes(req.Context)
			if len(nodes) > 0 {
				return FunctionCall{
					Name: PlanFunctionName,
					Arguments: map[string]any{
						"goal":      "Plan a valid next-hop sequence for the current task.",
						"nodes":     toAnySlice(nodes),
						"edges":     toPlanEdges(nodes),
						"reasoning": "Build a lightweight runtime plan before routing concrete hops.",
						"data":      map[string]any{"source": "mock_function_provider"},
					},
				}, 200, nil
			}
		}
	}

	nextNode, reasoning, data, err := mockRoute(req.Context)
	if err != nil {
		return FunctionCall{}, 0, err
	}
	return FunctionCall{
		Name: RouteFunctionName,
		Arguments: map[string]any{
			"next_node": nextNode,
			"reasoning": reasoning,
			"data":      data,
		},
	}, 200, nil
}

func mockPlanNodes(ctx RoutingContext) []string {
	for _, nodeID := range []string{"resolve_user_location", "query_weather", "finalize_weather_answer"} {
		if candidateNodeExists(ctx.CandidateNodes, nodeID) {
			continue
		}
		return nil
	}
	return []string{"resolve_user_location", "query_weather", "finalize_weather_answer"}
}

func toPlanEdges(nodes []string) []any {
	if len(nodes) < 2 {
		return nil
	}
	edges := make([]any, 0, len(nodes)-1)
	for idx := 0; idx < len(nodes)-1; idx++ {
		edges = append(edges, map[string]any{
			"from": nodes[idx],
			"to":   nodes[idx+1],
		})
	}
	return edges
}

func toAnySlice(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func mockRoute(ctx RoutingContext) (string, string, map[string]any, error) {
	snapshot := ctx.State.Snapshot()
	text := strings.ToLower(strings.TrimSpace(ctx.UserInput))

	if hasWeatherWorkflow(ctx.CandidateNodes) {
		location, _ := snapshot.WorkingMemory["location"].(string)
		weather, _ := snapshot.WorkingMemory["weather"].(string)
		finalText, _ := snapshot.WorkingMemory["final_text"].(string)

		if location == "" && containsAny(text, "天气", "weather", "带伞", "下雨") {
			return "resolve_user_location", "The weather workflow requires a location first.", map[string]any{}, nil
		}
		if location != "" && weather == "" {
			return "query_weather", "Location is ready; fetch weather before drafting the answer.", map[string]any{
				"location": location,
			}, nil
		}
		if weather != "" && finalText == "" {
			return "finalize_weather_answer", "Weather data is available; generate the final response.", map[string]any{
				"location": location,
				"weather":  weather,
			}, nil
		}
		return terminateNode, "Weather workflow already produced the final answer.", map[string]any{}, nil
	}

	if _, ok := snapshot.NodeOutputs["intent_parse"]; !ok && candidateNodeExists(ctx.CandidateNodes, "intent_parse") {
		return "intent_parse", "Need to parse the incoming user intent.", map[string]any{}, nil
	}
	if _, ok := snapshot.NodeOutputs["text_transform"]; !ok && candidateNodeExists(ctx.CandidateNodes, "text_transform") {
		return "text_transform", "Transform text before finalization.", map[string]any{}, nil
	}
	if _, ok := snapshot.NodeOutputs["finalize"]; ok && candidateNodeExists(ctx.CandidateNodes, "finalize") {
		return terminateNode, "Final answer already exists; terminate the execution chain.", map[string]any{}, nil
	}
	if candidateNodeExists(ctx.CandidateNodes, "finalize") {
		return "finalize", "All prerequisite information is ready.", map[string]any{}, nil
	}

	return "", "", nil, errors.New("mock_function provider cannot determine the next node")
}

func hasWeatherWorkflow(candidates []CandidateNode) bool {
	required := []string{"resolve_user_location", "query_weather", "finalize_weather_answer"}
	for _, nodeID := range required {
		if !candidateNodeExists(candidates, nodeID) {
			return false
		}
	}
	return true
}

func containsAny(input string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(input, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

type openAIProvider struct {
	kind string
}

func (p openAIProvider) Name() string { return p.kind }

func (p openAIProvider) Invoke(ctx context.Context, cfg config.ModelConfig, req Request) (FunctionCall, int, error) {
	payload, err := buildOpenAIRequest(ctx, cfg, req)
	if err != nil {
		return FunctionCall{}, 0, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("create openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKeyEnv != "" {
		httpReq.Header.Set("Authorization", "Bearer "+os.Getenv(cfg.APIKeyEnv))
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return FunctionCall{}, resp.StatusCode, fmt.Errorf("read openai response: %w", err)
	}
	functionCall, err := parseOpenAIFunctionCall(data)
	if err != nil {
		return FunctionCall{}, resp.StatusCode, err
	}
	return functionCall, resp.StatusCode, nil
}

type anthropicProvider struct{}

func (anthropicProvider) Name() string { return "anthropic" }

func (anthropicProvider) Invoke(ctx context.Context, cfg config.ModelConfig, req Request) (FunctionCall, int, error) {
	payload, err := buildAnthropicRequest(cfg, req)
	if err != nil {
		return FunctionCall{}, 0, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("create anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKeyEnv != "" {
		httpReq.Header.Set("x-api-key", os.Getenv(cfg.APIKeyEnv))
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return FunctionCall{}, 0, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return FunctionCall{}, resp.StatusCode, fmt.Errorf("read anthropic response: %w", err)
	}
	functionCall, err := parseAnthropicFunctionCall(data)
	if err != nil {
		return FunctionCall{}, resp.StatusCode, err
	}
	return functionCall, resp.StatusCode, nil
}

func buildOpenAIRequest(ctx context.Context, cfg config.ModelConfig, req Request) (map[string]any, error) {
	contextJSON, err := routingContextJSON(req.Context)
	if err != nil {
		return nil, err
	}

	tools := buildFunctionTools(req.Context)
	return map[string]any{
		"model": cfg.Model,
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "You are a routing model. You must answer with exactly one function call and no free-form text.",
			},
			{
				"role":    "user",
				"content": contextJSON,
			},
		},
		"tools":               tools,
		"tool_choice":         "required",
		"parallel_tool_calls": false,
		"metadata": map[string]any{
			"trace_id": platform.TraceIDFromContext(ctx),
		},
	}, nil
}

func buildAnthropicRequest(cfg config.ModelConfig, req Request) (map[string]any, error) {
	contextJSON, err := routingContextJSON(req.Context)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"model":      cfg.Model,
		"max_tokens": 1024,
		"system":     "You are a routing model. You must answer with exactly one tool use and no free-form text.",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": contextJSON,
					},
				},
			},
		},
		"tools": buildAnthropicTools(req.Context),
	}, nil
}

func routingContextJSON(ctx RoutingContext) (string, error) {
	stateMap, err := ctx.State.ToMap()
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"task_id":               ctx.TaskID,
		"user_input":            ctx.UserInput,
		"keywords":              ctx.Keywords,
		"state":                 stateMap,
		"candidate_nodes":       ctx.CandidateNodes,
		"recommended_nodes":     ctx.RecommendedNodes,
		"last_rejection_reason": ctx.LastRejectionReason,
		"planning_enabled":      ctx.PlanningEnabled,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal routing context: %w", err)
	}
	return string(raw), nil
}

func buildFunctionTools(ctx RoutingContext) []map[string]any {
	defs := BuildFunctionDefs(ctx)
	tools := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        def.Name,
				"description": def.Description,
				"parameters":  def.JSONSchema,
			},
		})
	}
	return tools
}

func buildAnthropicTools(ctx RoutingContext) []map[string]any {
	defs := BuildFunctionDefs(ctx)
	tools := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, map[string]any{
			"name":         def.Name,
			"description":  def.Description,
			"input_schema": def.JSONSchema,
		})
	}
	return tools
}

func BuildFunctionDefs(ctx RoutingContext) []FunctionDef {
	defs := []FunctionDef{
		{
			Name:        RouteFunctionName,
			Description: "Select the next executable node for the runtime scheduler.",
			JSONSchema:  buildRouteDecisionSchema(ctx.CandidateNodes),
		},
	}
	if ctx.PlanningEnabled {
		defs = append(defs, FunctionDef{
			Name:        PlanFunctionName,
			Description: "Propose a DAG-shaped plan without executing it directly.",
			JSONSchema:  buildPlanSchema(ctx.CandidateNodes),
		})
	}
	return defs
}

func BuildOpenAITools(ctx RoutingContext) []map[string]any {
	return buildFunctionTools(ctx)
}

func BuildAnthropicTools(ctx RoutingContext) []map[string]any {
	return buildAnthropicTools(ctx)
}

func buildRouteDecisionSchema(candidates []CandidateNode) map[string]any {
	enum := make([]any, 0, len(candidates)+1)
	enum = append(enum, terminateNode)
	for _, candidate := range candidates {
		enum = append(enum, candidate.ID)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"next_node": map[string]any{
				"type":        "string",
				"enum":        enum,
				"description": "Identifier of the next node to execute. Use __terminate__ to stop the runtime loop.",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Short explanation for why this node should run next.",
			},
			"data": map[string]any{
				"type":                 "object",
				"description":          "Structured routing metadata for auditing or downstream use.",
				"additionalProperties": true,
			},
		},
		"required": []any{"next_node", "reasoning", "data"},
	}
}

func buildPlanSchema(candidates []CandidateNode) map[string]any {
	enum := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		enum = append(enum, candidate.ID)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"goal": map[string]any{
				"type":        "string",
				"description": "Planning goal for the current task.",
			},
			"nodes": map[string]any{
				"type":        "array",
				"description": "Candidate node identifiers that should appear in the proposed plan.",
				"items": map[string]any{
					"type":        "string",
					"enum":        enum,
					"description": "A node identifier from the current candidate set.",
				},
			},
			"edges": map[string]any{
				"type":        "array",
				"description": "Directed edges between planned nodes. This is advisory only and does not bypass runtime validation.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"from": map[string]any{
							"type":        "string",
							"enum":        enum,
							"description": "Source node identifier.",
						},
						"to": map[string]any{
							"type":        "string",
							"enum":        enum,
							"description": "Target node identifier.",
						},
					},
					"required": []any{"from", "to"},
				},
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Short explanation for the proposed DAG.",
			},
			"data": map[string]any{
				"type":                 "object",
				"description":          "Structured planning metadata.",
				"additionalProperties": true,
			},
		},
		"required": []any{"goal", "nodes", "edges", "reasoning", "data"},
	}
}

func parseOpenAIFunctionCall(data []byte) (FunctionCall, error) {
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return FunctionCall{}, err
	}

	if call, ok := extractFunctionCall(envelope["tool_calls"]); ok {
		return call, nil
	}

	choices, ok := envelope["choices"].([]any)
	if ok && len(choices) > 0 {
		first, ok := choices[0].(map[string]any)
		if ok {
			message, ok := first["message"].(map[string]any)
			if ok {
				if call, ok := extractFunctionCall(message["tool_calls"]); ok {
					return call, nil
				}
			}
		}
	}

	output, ok := envelope["output"].([]any)
	if ok {
		for _, item := range output {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if entryType, _ := entry["type"].(string); entryType == "function_call" {
				call, err := extractFunctionCallFromEntry(entry)
				if err != nil {
					return FunctionCall{}, err
				}
				return call, nil
			}
		}
	}

	return FunctionCall{}, errors.New("openai response did not contain a function call")
}

func parseAnthropicFunctionCall(data []byte) (FunctionCall, error) {
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return FunctionCall{}, err
	}
	content, ok := envelope["content"].([]any)
	if !ok {
		return FunctionCall{}, errors.New("anthropic response content is missing")
	}
	var found FunctionCall
	foundCount := 0
	for _, item := range content {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entryType, _ := entry["type"].(string)
		if entryType != "tool_use" {
			continue
		}
		input, _ := entry["input"].(map[string]any)
		call := FunctionCall{
			Name:      strings.TrimSpace(fmt.Sprint(entry["name"])),
			Arguments: input,
			CallID:    strings.TrimSpace(fmt.Sprint(entry["id"])),
		}
		found = call
		foundCount++
	}
	if foundCount == 0 {
		return FunctionCall{}, errors.New("anthropic response did not contain a tool_use block")
	}
	if foundCount > 1 {
		return FunctionCall{}, errors.New("anthropic response contained multiple tool_use blocks")
	}
	if found.Arguments == nil {
		found.Arguments = map[string]any{}
	}
	return found, nil
}

func extractFunctionCall(raw any) (FunctionCall, bool) {
	items, ok := raw.([]any)
	if !ok {
		return FunctionCall{}, false
	}
	if len(items) != 1 {
		return FunctionCall{}, false
	}
	entry, ok := items[0].(map[string]any)
	if !ok {
		return FunctionCall{}, false
	}
	call, err := extractFunctionCallFromEntry(entry)
	if err != nil {
		return FunctionCall{}, false
	}
	return call, true
}

func extractFunctionCallFromEntry(entry map[string]any) (FunctionCall, error) {
	payload := entry
	if nested, ok := entry["function"].(map[string]any); ok {
		payload = nested
	}
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return FunctionCall{}, errors.New("function call name must not be empty")
	}
	callID := strings.TrimSpace(fmt.Sprint(entry["id"]))
	if callID == "<nil>" {
		callID = ""
	}

	switch args := payload["arguments"].(type) {
	case string:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return FunctionCall{}, fmt.Errorf("parse function call arguments: %w", err)
		}
		return FunctionCall{Name: name, Arguments: parsed, CallID: callID}, nil
	case map[string]any:
		return FunctionCall{Name: name, Arguments: args, CallID: callID}, nil
	default:
		return FunctionCall{}, errors.New("function call arguments must be a json object")
	}
}
