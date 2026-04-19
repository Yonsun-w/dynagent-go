package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/admin/ai_project/internal/app"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
)

// 这个 demo 直接复用 DynAgent 框架本身：
// 1. 加载配置并初始化 app
// 2. 注册天气场景节点
// 3. 让 AI 网关通过 function calling 选择下一跳
// 4. 由 engine 负责调度、沙箱执行、状态合并、摘要输出
//
// 这里没有额外再造一套 ToolRegistry / Agent / State / Scheduler。

const (
	nodeResolveLocation = "resolve_user_location"
	nodeQueryWeather    = "query_weather"
	nodeFinalizeWeather = "finalize_weather_answer"
)

func main() {
	var configPath string
	var prompt string

	flag.StringVar(&configPath, "config", "./configs/config.yaml", "配置文件路径")
	flag.StringVar(&prompt, "prompt", "帮我查一下我当前位置的天气，并告诉我要不要带伞。", "用户输入")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// demo 默认打开规划能力，方便把 propose_dag(...) 和 route_next_node(...) 都走一遍。
	cfg.AI.RoutingMode = "route_and_plan"

	ctx := context.Background()
	application, err := app.New(ctx, cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "初始化应用失败: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = application.Close(context.Background())
	}()

	for _, n := range []node.Node{
		resolveUserLocationNode{},
		queryWeatherNode{},
		finalizeWeatherAnswerNode{},
	} {
		if err := application.Registry.RegisterBuiltin(n); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "注册节点失败: %v\n", err)
			os.Exit(1)
		}
	}

	taskID := fmt.Sprintf("weather-demo-%d", time.Now().UnixNano())
	st, err := state.New(taskID, platform.NewTraceID(), state.UserInput{
		Text:     prompt,
		Keywords: []string{"weather", "demo"},
		Ext:      map[string]any{"scene": "weather_demo"},
	}, map[string]string{"demo": "weather"})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "初始化状态失败: %v\n", err)
		os.Exit(1)
	}

	summaryPayload, err := application.Engine.Run(ctx, st)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "执行任务失败: %v\n", err)
		os.Exit(1)
	}

	record, err := application.Store.GetTask(ctx, taskID)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "读取任务记录失败: %v\n", err)
		os.Exit(1)
	}

	// 这里打印的是框架真实产物：
	// - summary：结构化摘要
	// - decision_log：函数调用轨迹
	// - node_outputs：节点结果
	payload := map[string]any{
		"task_id":        taskID,
		"routing_mode":   cfg.AI.RoutingMode,
		"summary":        summaryPayload,
		"decision_log":   record.State.DecisionLog,
		"node_outputs":   record.State.NodeOutputs,
		"working_memory": record.State.WorkingMemory,
	}

	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "序列化输出失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(raw))
}

// resolveUserLocationNode 模拟“查询用户当前位置”。
// 节点只返回 patch，由引擎统一合并进主 State。
type resolveUserLocationNode struct{}

func (resolveUserLocationNode) Meta() node.Meta {
	return node.Meta{
		ID:           nodeResolveLocation,
		Version:      "v1",
		Description:  "Resolve the current user location before weather lookup.",
		Labels:       []string{"weather", "tool", "location"},
		InputSchema:  node.Schema{Required: []string{"user_input.text"}},
		OutputSchema: node.Schema{Required: []string{"location"}},
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

// queryWeatherNode 模拟“根据当前位置查询天气”。
// 这里不真正调用天气 API，只返回稳定的 mock 结果。
type queryWeatherNode struct{}

func (queryWeatherNode) Meta() node.Meta {
	return node.Meta{
		ID:           nodeQueryWeather,
		Version:      "v1",
		Description:  "Query weather data by location.",
		Labels:       []string{"weather", "tool"},
		InputSchema:  node.Schema{Required: []string{"working_memory.location"}},
		OutputSchema: node.Schema{Required: []string{"weather"}},
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

// finalizeWeatherAnswerNode 负责根据位置和天气生成最终结论。
type finalizeWeatherAnswerNode struct{}

func (finalizeWeatherAnswerNode) Meta() node.Meta {
	return node.Meta{
		ID:           nodeFinalizeWeather,
		Version:      "v1",
		Description:  "Generate the final weather answer for the user.",
		Labels:       []string{"weather", "terminal"},
		InputSchema:  node.Schema{Required: []string{"working_memory.location", "working_memory.weather"}},
		OutputSchema: node.Schema{Required: []string{"final_text"}},
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
