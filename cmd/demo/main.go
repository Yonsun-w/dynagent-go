package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// 这个 demo 的目标不是复用完整框架装配，而是把“一个动态 Agent 的关键机制”
// 用一个文件完整穿起来，方便二次开发时快速看懂：
// 1. 统一 AI Service 抽象
// 2. Tool 定义与注册
// 3. Agent State
// 4. LLM 驱动的动态路由
// 5. 最大步数保护，防止死循环
// 6. 最终结果输出

const (
	toolLocateUser  = "get_user_location"
	toolQueryWeather = "get_weather"
	finishNode      = "__finish__"
)

// Decision 是 AI Service 对调度器返回的统一决策结构。
// 它和框架主工程里的思想一致：AI 只负责建议下一跳，不直接操作主状态。
type Decision struct {
	NextTool  string         `json:"next_tool"`
	Reasoning string         `json:"reasoning"`
	Data      map[string]any `json:"data"`
}

// AIService 抽象“模型服务层”。
// 真实项目里这里可以接 OpenAI / Anthropic / 自建网关。
type AIService interface {
	Decide(ctx context.Context, req AIRequest) (Decision, error)
}

// AIRequest 是发给模型的上下文。
// 这里只保留 demo 需要的关键字段：用户 prompt、当前状态、可选工具列表。
type AIRequest struct {
	UserPrompt string
	State      AgentState
	ToolMetas  []ToolMeta
}

// Tool 是最小工具接口。
// Tool 不直接修改主状态，只返回结构化结果，由主调度器统一写回。
type Tool interface {
	Meta() ToolMeta
	Invoke(ctx context.Context, state AgentState) (ToolResult, error)
}

// ToolMeta 描述工具本身。
type ToolMeta struct {
	Name        string
	Description string
}

// ToolResult 是工具执行结果。
// 输出会被主循环合并回 AgentState。
type ToolResult struct {
	Output map[string]any
}

// AgentState 是当前任务的全局状态。
// 在真实框架里它会更复杂，这里只保留天气查询场景必须字段。
type AgentState struct {
	UserPrompt  string         `json:"user_prompt"`
	Location    string         `json:"location"`
	Weather     string         `json:"weather"`
	FinalAnswer string         `json:"final_answer"`
	Steps       []StepRecord   `json:"steps"`
	Memory      map[string]any `json:"memory"`
}

// StepRecord 记录每一步执行轨迹，方便打印和复盘。
type StepRecord struct {
	Step      int               `json:"step"`
	Tool      string            `json:"tool"`
	Reasoning string            `json:"reasoning"`
	Output    map[string]any    `json:"output"`
	At        time.Time         `json:"at"`
}

// ToolRegistry 负责工具注册与查询。
type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

func (r *ToolRegistry) Register(tool Tool) error {
	meta := tool.Meta()
	if strings.TrimSpace(meta.Name) == "" {
		return errors.New("tool name must not be empty")
	}
	r.tools[meta.Name] = tool
	return nil
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) ListMeta() []ToolMeta {
	result := make([]ToolMeta, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool.Meta())
	}
	return result
}

// mockAIService 用非常轻量的规则模拟“LLM 根据 prompt + state 进行智能路由”。
// 这里故意不写死执行图，而是让 AI 根据当前状态决定下一步：
// - 没有 location → 先查位置
// - 有位置没天气 → 查天气
// - 有天气 → 生成最终回答并结束
type mockAIService struct{}

func (mockAIService) Decide(ctx context.Context, req AIRequest) (Decision, error) {
	_ = ctx

	// 这里就是“结合用户 prompt 让 LLM 做路由”的最小表现形式。
	// 真实模型版会把 req.UserPrompt + req.State + req.ToolMetas 打成 prompt 发给模型。
	lowerPrompt := strings.ToLower(req.UserPrompt)

	if req.State.Location == "" && (strings.Contains(lowerPrompt, "天气") || strings.Contains(lowerPrompt, "weather")) {
		return Decision{
			NextTool:  toolLocateUser,
			Reasoning: "用户询问天气，但当前状态里没有位置信息，先查询用户当前位置。",
			Data:      map[string]any{},
		}, nil
	}

	if req.State.Location != "" && req.State.Weather == "" {
		return Decision{
			NextTool:  toolQueryWeather,
			Reasoning: "已经拿到位置，下一步查询该位置天气。",
			Data: map[string]any{
				"location": req.State.Location,
			},
		}, nil
	}

	if req.State.Weather != "" {
		final := fmt.Sprintf("你当前在%s，天气%s。建议带伞，穿一件轻薄外套。", req.State.Location, req.State.Weather)
		return Decision{
			NextTool:  finishNode,
			Reasoning: "位置和天气都已经齐全，可以直接生成最终回答。",
			Data: map[string]any{
				"final_answer": final,
			},
		}, nil
	}

	return Decision{}, errors.New("AI 无法根据当前状态决定下一步")
}

// locateUserTool 模拟“查询用户当前位置”的工具。
// 这里不做真实定位，只返回一个 mock 位置，重点是演示工具协议。
type locateUserTool struct{}

func (locateUserTool) Meta() ToolMeta {
	return ToolMeta{
		Name:        toolLocateUser,
		Description: "查询用户当前位置。这里用 mock 数据代替真实定位能力。",
	}
}

func (locateUserTool) Invoke(ctx context.Context, state AgentState) (ToolResult, error) {
	_ = ctx
	_ = state
	return ToolResult{
		Output: map[string]any{
			"location": "上海市浦东新区",
		},
	}, nil
}

// queryWeatherTool 模拟“查询天气”的工具。
// 这里同样不调真实天气服务，只返回假数据。
type queryWeatherTool struct{}

func (queryWeatherTool) Meta() ToolMeta {
	return ToolMeta{
		Name:        toolQueryWeather,
		Description: "根据 location 查询天气。这里用 mock 天气数据代替真实天气 API。",
	}
}

func (queryWeatherTool) Invoke(ctx context.Context, state AgentState) (ToolResult, error) {
	_ = ctx
	if strings.TrimSpace(state.Location) == "" {
		return ToolResult{}, errors.New("location 为空，无法查询天气")
	}
	return ToolResult{
		Output: map[string]any{
			"weather": "多云，24°C，东南风 3 级",
		},
	}, nil
}

// WeatherAgent 是一个最小可运行 Agent 调度器。
// 它体现的就是这个框架的核心思想：
// AI 决策下一跳 -> 执行工具 -> 合并状态 -> 继续下一轮。
type WeatherAgent struct {
	ai       AIService
	registry *ToolRegistry
	maxSteps int
}

func NewWeatherAgent(ai AIService, registry *ToolRegistry, maxSteps int) *WeatherAgent {
	return &WeatherAgent{
		ai:       ai,
		registry: registry,
		maxSteps: maxSteps,
	}
}

func (a *WeatherAgent) Run(ctx context.Context, prompt string) (AgentState, error) {
	state := AgentState{
		UserPrompt: prompt,
		Memory:     map[string]any{},
	}

	for step := 1; step <= a.maxSteps; step++ {
		decision, err := a.ai.Decide(ctx, AIRequest{
			UserPrompt: prompt,
			State:      state,
			ToolMetas:  a.registry.ListMeta(),
		})
		if err != nil {
			return state, fmt.Errorf("第 %d 步 AI 决策失败: %w", step, err)
		}

		// 如果 AI 判断任务已完成，则直接写入最终答案并结束。
		if decision.NextTool == finishNode {
			if finalAnswer, ok := decision.Data["final_answer"].(string); ok {
				state.FinalAnswer = finalAnswer
			}
			state.Steps = append(state.Steps, StepRecord{
				Step:      step,
				Tool:      finishNode,
				Reasoning: decision.Reasoning,
				Output:    map[string]any{"final_answer": state.FinalAnswer},
				At:        time.Now(),
			})
			return state, nil
		}

		tool, ok := a.registry.Get(decision.NextTool)
		if !ok {
			return state, fmt.Errorf("第 %d 步工具不存在: %s", step, decision.NextTool)
		}

		result, err := tool.Invoke(ctx, state)
		if err != nil {
			return state, fmt.Errorf("第 %d 步工具执行失败(%s): %w", step, decision.NextTool, err)
		}

		// 主调度器统一合并状态。工具本身不直接改 state。
		state = mergeState(state, decision.NextTool, decision.Reasoning, result.Output, step)
	}

	return state, fmt.Errorf("超过最大执行步数 %d，已强制终止，防止 Agent 卡死", a.maxSteps)
}

func mergeState(state AgentState, toolName string, reasoning string, output map[string]any, step int) AgentState {
	if location, ok := output["location"].(string); ok {
		state.Location = location
	}
	if weather, ok := output["weather"].(string); ok {
		state.Weather = weather
	}
	for key, value := range output {
		state.Memory[key] = value
	}
	state.Steps = append(state.Steps, StepRecord{
		Step:      step,
		Tool:      toolName,
		Reasoning: reasoning,
		Output:    output,
		At:        time.Now(),
	})
	return state
}

func main() {
	// prompt 模拟用户请求；你也可以通过命令行覆盖。
	defaultPrompt := "帮我查一下我现在这个位置的天气，并给我一个简短的出门建议。"

	var prompt string
	var maxSteps int
	flag.StringVar(&prompt, "prompt", defaultPrompt, "用户输入 prompt")
	flag.IntVar(&maxSteps, "max-steps", 6, "最大执行步数，用于防止 Agent 死循环")
	flag.Parse()

	ctx := context.Background()

	// 1. 初始化 AI Service。真实项目里这里可以替换成 OpenAI / Anthropic / 自建网关。
	aiService := mockAIService{}

	// 2. 定义并注册工具。
	toolRegistry := NewToolRegistry()
	if err := toolRegistry.Register(locateUserTool{}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "注册工具失败: %v\n", err)
		os.Exit(1)
	}
	if err := toolRegistry.Register(queryWeatherTool{}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "注册工具失败: %v\n", err)
		os.Exit(1)
	}

	// 3. 创建 Agent，并设置最大步数保护，防止路由异常导致卡死。
	agent := NewWeatherAgent(aiService, toolRegistry, maxSteps)

	// 4. 执行任务。
	finalState, err := agent.Run(ctx, prompt)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Agent 执行失败: %v\n", err)
		os.Exit(1)
	}

	// 5. 输出完整执行结果，方便观察整个链路。
	payload := map[string]any{
		"user_prompt":  finalState.UserPrompt,
		"location":     finalState.Location,
		"weather":      finalState.Weather,
		"final_answer": finalState.FinalAnswer,
		"steps":        finalState.Steps,
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Println(string(raw))
}
