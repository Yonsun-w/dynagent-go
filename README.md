# DynAgent 🧠⚙️

> 一个 Go 原生、无预设拓扑、Function Calling-only 的动态 Agent 运行时内核。

[English](#english) | [接入指南](./docs/integration.zh-CN.md) | [架构说明](./docs/architecture.zh-CN.md) | [设计方案](./docs/design.zh-CN.md) | [Architecture EN](./docs/architecture.en.md) | [Design EN](./docs/design.en.md)

## 先用一句话说清楚

DynAgent 不是工作流搭建器，也不是固定 DAG 的编排框架。

它做的事很简单：

- 把一组可执行节点注册进运行时
- 把当前任务状态和候选节点交给大模型
- 让模型通过 **Function Calling** 选择下一跳
- 由 Runtime 负责准入校验、沙箱执行、状态合并、持久化和回放

如果你想做的是“**让 LLM 决定下一步该调用哪个能力，但执行和状态必须由框架兜底**”，这个项目就是干这个的。

## 它能解决什么问题

很多 Agent 项目在这几个地方会失控：

- 节点跳转关系写死，越改越像流程图
- 节点随手改全局状态，最后很难排查问题
- LLM 输出不稳定，协议一变就炸
- 线上跑起来后，看不到完整执行轨迹

DynAgent 的做法是：

- **没有预设边**：节点之间不写死跳转关系
- **只有调度器能改主状态**：节点只返回 `Patch`
- **只接受 Function Calling**：不吃自由文本决策
- **每一步都可追踪**：决策、快照、摘要、血缘都能落库
- **每个节点都进沙箱**：超时、panic recover、并发限制默认开启

## 现在仓库里已经有什么

当前仓库已经不是空架子，已经有这些能力：

- 动态调度引擎
- AI Gateway
- 节点注册中心
- Goroutine 沙箱执行器
- 全局 State + Patch 合并
- 快照、步骤、摘要、回放、续跑
- HTTP 服务入口
- 一个可直接跑通的天气 Agent demo

## 5 分钟跑通

### 1. 本地直接跑 mock demo

```bash
source ~/.gvm/scripts/gvm && gvm use go1.22.12 >/dev/null
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml \
  --prompt '帮我查一下我当前位置的天气，并告诉我要不要带伞'
```

这会跑一个完整天气链路：

1. `resolve_user_location`
2. `query_weather`
3. `finalize_weather_answer`
4. `__terminate__`

### 2. 接真实 LLM API 跑

把配置改成真实 provider，例如：

```yaml
ai:
  routing_mode: route_and_plan
  primary:
    provider: openai # 也支持 qwen / kimi / glm
    endpoint: "https://your-provider-endpoint"
    model: "your-model"
    api_key_env: "LLM_API_KEY"
    timeout: 10s
  fallback:
    provider: mock_function
    endpoint: ""
    model: "mock-function-fallback"
    api_key_env: ""
    timeout: 5s
```

然后运行：

```bash
export LLM_API_KEY="your-api-key"

source ~/.gvm/scripts/gvm && gvm use go1.22.12 >/dev/null
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml \
  --prompt '帮我查一下我当前位置的天气，并告诉我要不要带伞' \
  --verbose
```

如果配置不完整，demo 会在启动前直接报错，不会等到运行过程中再给你一个模糊的 provider 错误。

## 这个 demo 到底演示了什么

当前 `cmd/demo` 不是“打印几段假 JSON”，而是走框架真实主链路：

1. 注册 3 个自定义天气节点
2. 构造任务 State
3. 生成候选节点列表和 `routing_context`
4. 把 `route_next_node(...)` / `propose_dag(...)` 注册给模型
5. 由 LLM 通过 function calling 决定下一跳
6. Runtime 做准入校验和沙箱执行
7. 节点返回 `Patch`
8. 调度器合并状态、记录快照、产出结构化摘要

默认输出会包含：

- `provider_info`
- `registered_nodes`
- `function_contracts`
- `llm_registration`
- `decision_trace`
- `node_outputs`
- `final_summary`

加上 `--verbose` 后，会再看到：

- `routing_context`
- `runtime_state`
- `anthropic_tools`
- 更完整的调试信息

## 最重要的设计约束

这个项目最核心的约束只有 4 条：

1. **没有预设拓扑**  
   Runtime 只暴露节点池，不定义固定边。

2. **只有调度器能改主状态**  
   节点只能读取 `ReadOnlyState`，并返回 `Patch`。

3. **LLM 决策必须走 Function Calling**  
   不兼容自由文本路由，也不兼容历史 JSON 猜测模式。

4. **执行必须有运行时保护**  
   超时、熔断、限流、fallback、panic recover、最大步数、循环检测都要有。

## Function Calling 协议

DynAgent 默认使用两个函数。

### 路由函数

```text
route_next_node(
  next_node: string,
  reasoning: string,
  data: object
)
```

- `next_node`：下一跳节点 ID，或 `__terminate__`
- `reasoning`：本次路由的解释
- `data`：结构化补充信息

### 可选规划函数

```text
propose_dag(
  goal: string,
  nodes: string[],
  edges: {from,to}[],
  reasoning: string,
  data: object
)
```

`propose_dag(...)` 只负责留下规划痕迹。真正执行仍然坚持“**一次一步**”。

## 二次开发怎么接

最短路径就是两步。

### 1. 写你的节点

你只需要实现这个接口：

```go
type Node interface {
    Meta() Meta
    CheckBefore(ctx context.Context, st *state.ReadOnlyState) CheckResult
    Execute(ctx context.Context, st *state.ReadOnlyState) Result
}
```

节点开发时记住两件事：

- 不要直接改主 State
- 只通过 `Result.Patch` 回写结果

天气 demo 里的节点实现可以直接拿来当模板：

- `resolve_user_location`
- `query_weather`
- `finalize_weather_answer`

### 2. 注册你的节点

```go
if err := app.Registry.RegisterBuiltin(yourNode{}); err != nil {
    panic(err)
}
```

之后 Runtime 会自动把这些节点放进候选池，让 LLM 决定下一跳。

## 架构图

### 总览

![DynAgent 现代化架构图](./docs/assets/architecture-modern-zh.svg)

### 控制环

![DynAgent 现代化运行时控制环](./docs/assets/runtime-flow-modern-zh.svg)

### 数据流转

![DynAgent AI 决策与数据流](./docs/assets/ai-decision-modern-zh.svg)

### 时序图

![DynAgent 时序视图](./docs/assets/sequence-view-modern-zh.svg)

## 和 Claude Code / LangGraph 的差异

| 维度 | DynAgent | Claude Code | LangGraph |
| --- | --- | --- | --- |
| 核心目标 | 通用动态 Agent Runtime | 编码场景 Agent 助手 | 图式 Agent 编排 |
| 拓扑 | 无预设边 | 动态任务流 | 显式图结构 |
| 状态所有权 | 调度器独占主 State | 会话 / 工作区中心 | 图执行器传递状态 |
| 决策协议 | Function Calling 选节点 | 工具使用偏编码场景 | 开发者定义图转移 |

## 仓库结构

```text
.
├── cmd/server                # HTTP 服务入口
├── cmd/demo                  # 天气 Agent demo
├── configs                   # 配置与动态节点 manifest
├── docs                      # 中英文文档与架构图
├── internal/ai              # AI Gateway 与 provider 适配
├── internal/engine          # 动态调度核心
├── internal/node            # 节点接口与注册中心
├── internal/sandbox         # 节点沙箱
├── internal/state           # State 与 Patch
├── internal/persistence     # 存储实现
└── examples/weatherdemo     # 示例：天气 Agent
```

## 文档入口

- [中文接入指南](./docs/integration.zh-CN.md)
- [中文架构说明](./docs/architecture.zh-CN.md)
- [中文设计方案](./docs/design.zh-CN.md)
- [English README](./docs/README.en.md)
- [English Architecture Guide](./docs/architecture.en.md)
- [English Design Spec](./docs/design.en.md)

## 当前验证

```bash
source ~/.gvm/scripts/gvm && gvm use go1.22.12 >/dev/null
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml \
  --prompt '帮我查一下我当前位置的天气，并告诉我要不要带伞'
```

## English

> A Go-native, topology-free, function-calling-only runtime kernel for dynamic Agents.

[中文](#dynagent-️) | [Architecture EN](./docs/architecture.en.md) | [Design EN](./docs/design.en.md)

### What It Is

DynAgent is not a workflow builder and not a fixed DAG orchestrator.

It does one thing:

- register executable nodes
- send current state + candidate nodes to the model
- let the model choose the next node via function calling
- let the runtime own validation, sandbox execution, state merge, persistence, and replay

### What You Can Run Right Now

The repository already includes:

- a dynamic scheduler
- an AI gateway
- node registry
- sandbox executor
- state bus + patch merge
- replay / resume / summary pipeline
- a runnable weather agent demo

### Quick Start

```bash
source ~/.gvm/scripts/gvm && gvm use go1.22.12 >/dev/null
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml \
  --prompt 'Check the weather for my current location and tell me whether I should bring an umbrella'
```

To run with a real model API:

```bash
export LLM_API_KEY="your-api-key"

source ~/.gvm/scripts/gvm && gvm use go1.22.12 >/dev/null
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml \
  --prompt 'Check the weather for my current location and tell me whether I should bring an umbrella' \
  --verbose
```

### Runtime Contract

DynAgent uses function calling only:

```text
route_next_node(next_node, reasoning, data)
propose_dag(goal, nodes, edges, reasoning, data)
```

### Extension Path

Implement a node:

```go
type Node interface {
    Meta() Meta
    CheckBefore(ctx context.Context, st *state.ReadOnlyState) CheckResult
    Execute(ctx context.Context, st *state.ReadOnlyState) Result
}
```

Then register it:

```go
if err := app.Registry.RegisterBuiltin(yourNode{}); err != nil {
    panic(err)
}
```

### Docs

- [Integration Guide](./docs/integration.zh-CN.md)
- [Architecture EN](./docs/architecture.en.md)
- [Design EN](./docs/design.en.md)
