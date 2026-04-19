# DynAgent 接入与二次开发指南 🛠️

> 面向“准备把这个仓库真正接起来用”的工程文档。

这份文档回答 4 个问题：

1. 这个框架现在到底能做什么？
2. 我第一次怎么把它跑起来？
3. 我要怎么接自己的模型、节点、业务能力？
4. 我做二次开发时应该改哪些地方？

---

## 1. 这个框架现在能做什么

先说结论：

DynAgent 当前已经是一个**可运行的动态 Agent 执行内核**，但它不是“开箱即用的全能业务 Agent 产品”。

### 当前已经具备的能力

- 接收一条任务输入并执行完整动态链路
- 由 AI 通过 function calling 决定下一跳节点
- 调度器校验节点存在性、准入规则、超时、循环和最大步数
- 节点只能读取 `ReadOnlyState`，不能直接改全局状态
- 节点结果通过 `Patch` 由调度器统一合并
- 为每一步生成执行记录、状态快照、结构化摘要
- 支持任务查询、摘要查询、回放、断点续跑
- 支持内置节点和外部节点两种扩展方式

### 当前默认 demo 会做什么

如果你直接运行 demo，现在跑的是一个**基于框架真实装配**的天气 Agent，默认链路是：

1. `propose_dag(...)`
2. `route_next_node(resolve_user_location)`
3. `route_next_node(query_weather)`
4. `route_next_node(finalize_weather_answer)`
5. `route_next_node(__terminate__)`

也就是说，当前仓库默认是“框架演示模式”，方便你确认：

- function calling 路由能跑通
- 状态合并能跑通
- 摘要与轨迹能跑通
- 规划记录与路由记录都能回放

但它**不是**已经内置大量真实业务节点的最终产品。

---

## 2. 第一次接起来怎么跑

### 2.1 本地运行 demo

```bash
CGO_ENABLED=0 go run ./cmd/demo --config ./configs/config.yaml
```

这条命令会：

- 初始化框架
- 注册天气场景节点
- 构造一个 demo 任务
- 跑完整条执行链
- 输出结构化摘要

适合第一次确认“框架主干是通的”。

### 2.2 启动 HTTP 服务

```bash
CGO_ENABLED=0 go run ./cmd/server --config ./configs/config.yaml
```

服务启动后可以调用：

```bash
curl -X POST http://localhost:8080/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "text": "Summarize this framework execution path.",
    "keywords": ["summarize", "framework", "execution"],
    "labels": {"source": "manual"}
  }'
```

然后继续查：

```bash
curl http://localhost:8080/v1/tasks/<task_id>
curl http://localhost:8080/v1/tasks/<task_id>/summary
curl http://localhost:8080/v1/tasks/<task_id>/replay
curl -X POST http://localhost:8080/v1/tasks/<task_id>/resume
curl http://localhost:8080/v1/nodes
```

### 2.3 这些接口分别做什么

- `POST /v1/tasks`
  创建任务并同步跑完整链路。
- `GET /v1/tasks/{id}`
  看任务状态、步骤、快照、摘要。
- `GET /v1/tasks/{id}/summary`
  只看结构化总结。
- `GET /v1/tasks/{id}/replay`
  看整条链路的步骤和快照，用于复盘。
- `POST /v1/tasks/{id}/resume`
  从最近快照恢复执行。
- `GET /v1/nodes`
  查看当前注册节点。

---

## 3. 这个框架建议怎么接入业务

最推荐的接入思路不是“把这个仓库改成一个具体业务系统”，而是：

把它当成一个**动态执行底座**，然后把你的业务能力封装成节点。

### 推荐接入模式

```text
你的业务系统
    ↓
HTTP / SDK 调 DynAgent
    ↓
DynAgent 跑动态节点链
    ↓
节点调用你的业务能力 / API / 数据源
    ↓
返回结构化结果
```

### 适合的业务类型

- 内容处理 Agent
- API 调度 Agent
- 数据加工 Agent
- 检索 + 处理 + 汇总 Agent
- 多工具调用 Agent
- 企业内部工作流智能执行内核

### 不适合直接拿来干的事

- 纯前端页面工作流搭建器
- 零代码可视化编排平台
- 已经强依赖固定 DAG 的场景

---

## 4. 二次开发的标准路径

二次开发通常是三步。

### Step 1：先接真实模型

当前默认配置里：

- `ai.primary.provider: mock_function`
- `ai.fallback.provider: mock_function`
- `ai.routing_mode: route_and_plan`

配置文件在 [config.yaml](/Users/admin/ai_project/configs/config.yaml)。

如果你要接真实模型，最先改的是这里：

```yaml
ai:
  primary:
    provider: openai
    endpoint: "https://your-endpoint"
    model: "your-model"
    api_key_env: "OPENAI_API_KEY"
    timeout: 5s
```

AI 网关实现位置在：

- [gateway.go](/Users/admin/ai_project/internal/ai/gateway.go)

当前已经有这些 provider 通道：

- `mock_function`
- `openai`
- `openai_compatible`
- `anthropic`

如果你的模型网关是 OpenAI 兼容协议，最简单的方式通常是走 `openai_compatible`。

#### 推荐的路由接法：固定 Function Calling

DynAgent 更推荐你把路由协议固定成一个函数，而不是让模型自由生成一段 JSON：

```text
route_next_node(
  next_node: string,
  reasoning: string,
  data: object
)
```

推荐原因很直接：

- 对模型约束更强，路由稳定性更高
- 更容易做 provider 兼容适配
- 更容易做日志采集、回放和错误排查

如果你真的需要“先规划 DAG”，建议显式启用第二个函数：

```text
propose_dag(
  goal: string,
  nodes: string[],
  edges: {from,to}[],
  reasoning: string,
  data: object
)
```

`propose_dag(...)` 只记录规划，不直接执行；  
真正执行时，调度器仍然只接收当前一步的 `route_next_node(...)` 结果。

### Step 2：把业务能力写成节点

节点接口在：

- [node.go](/Users/admin/ai_project/internal/node/node.go)

你要实现的是：

```go
type Node interface {
    Meta() Meta
    CheckBefore(ctx context.Context, st *state.ReadOnlyState) CheckResult
    Execute(ctx context.Context, st *state.ReadOnlyState) Result
}
```

#### 这三个方法分别负责什么

- `Meta()`
  定义节点 ID、说明、标签、输入输出 schema。
- `CheckBefore()`
  决定“当前状态下，这个节点允不允许进执行”。
- `Execute()`
  真正跑业务逻辑，读只读状态，返回 `Result`。

#### 节点开发时最重要的约束

- 不要直接改主 `State`
- 不要假设一定从某个固定前置节点进入
- 只依赖当前 `ReadOnlyState`
- 结果通过 `Patch` 返回

### Step 3：让调度器能发现你的节点

有两种接入方式。

#### 方式 A：做内置节点

适合：

- 逻辑简单
- 跟主服务一起发布
- 不需要独立热更新

做法：

1. 在 `plugins/builtin` 下新增一个节点文件
2. 实现 `Node` 接口
3. 在 [register.go](/Users/admin/ai_project/plugins/builtin/register.go) 里注册

最适合早期开发阶段。

#### 方式 B：做外部节点

适合：

- 需要热加载
- 需要独立发布
- 想和主调度器隔离进程

做法：

1. 写一个独立进程
2. 暴露 `pkg/contracts.NodeRuntime` gRPC 服务
3. 在 `configs/nodes.d` 放一个 manifest
4. 主服务自动发现并接入

外部节点示例代码在：

- [cmd/node-runner/main.go](/Users/admin/ai_project/cmd/node-runner/main.go)

manifest 示例在：

- [external_echo.yaml](/Users/admin/ai_project/configs/nodes.d/external_echo.yaml)

---

## 5. 内置节点二开示例

假设你要新增一个“提取订单号”的节点。

### 5.1 写节点文件

建议放到：

```text
plugins/builtin/order_extract.go
```

示例骨架：

```go
type orderExtractNode struct{}

func (orderExtractNode) Meta() node.Meta {
    return node.Meta{
        ID:          "order_extract",
        Version:     "v1",
        Description: "Extract order id from user text",
        Labels:      []string{"builtin", "parser"},
        InputSchema: node.Schema{Required: []string{"user_input.text"}},
        OutputSchema: node.Schema{Required: []string{"order_id"}},
    }
}

func (orderExtractNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
    if strings.TrimSpace(st.UserText()) == "" {
        return node.CheckResult{Allowed: false, Reason: "empty text"}
    }
    return node.CheckResult{Allowed: true}
}

func (orderExtractNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
    orderID := extractOrderID(st.UserText())
    return node.Result{
        Success: true,
        Output: map[string]any{"order_id": orderID},
        Patch: state.Patch{
            WorkingMemory: map[string]any{"order_id": orderID},
            NodeOutputs: map[string]map[string]any{
                "order_extract": {"order_id": orderID},
            },
        },
    }
}
```

### 5.2 注册节点

在 [register.go](/Users/admin/ai_project/plugins/builtin/register.go) 里加上：

```go
orderExtractNode{},
```

### 5.3 让 AI 有机会路由到它

有两种方式：

- 真实模型：通过函数 schema + 节点描述 + 状态上下文，让模型在 `route_next_node(...)` 中选择它
- 当前 demo：你可以扩展 `mock_function` provider 的函数调用策略

---

## 6. 外部节点接入示例

如果你想让节点独立进程运行，流程是这样的：

### 6.1 写 runtime server

最简单的方式就是参考：

- [cmd/node-runner/main.go](/Users/admin/ai_project/cmd/node-runner/main.go)

它做了三件事：

- 接收 gRPC 请求
- 把请求里的 state map 还原成 `State`
- 调用节点的 `CheckBefore` / `Execute`

### 6.2 配置 manifest

示例：

```yaml
id: external_echo
version: v1
description: External echo node
address: "127.0.0.1:9091"
autostart: true
command: "go"
args:
  - "run"
  - "./cmd/node-runner"
  - "--manifest"
  - "./configs/nodes.d/external_echo.yaml"
handler: "external_echo"
timeout: 5s
```

### 6.3 启动主服务

主服务会监听 `configs/nodes.d`：

- 新增 manifest → 自动加载
- 修改 manifest → 自动重载
- 删除 manifest → 自动摘除

这部分逻辑在：

- [node.go](/Users/admin/ai_project/internal/node/node.go)

---

## 7. 真实项目里通常怎么改

### 路线 A：把它当执行底座

这是最推荐的方式。

做法：

- 保留 DynAgent 作为独立 runtime 服务
- 你的业务系统通过 API 调它
- 你的业务能力以节点形式接入

优点：

- 边界清晰
- 可观测性集中
- 节点可以独立演进

### 路线 B：直接嵌进业务服务

做法：

- 在你的主服务里直接引用 `internal/app` 的装配逻辑
- 作为一个内部 runtime 模块使用

优点：

- 部署简单

缺点：

- 和业务系统耦合更高

---

## 8. 当前最值得优先补的二开点

如果你准备真正落业务，我建议先改这几块。

### 8.1 接真实模型

这是第一优先级，否则当前只能跑 mock function-calling demo。

### 8.2 增加业务节点

当前内置节点更多是演示用途，不足以支撑真实业务链。

### 8.3 改进 Routing Context

真实业务里，你应该重点增强的是**结构化路由上下文**，而不是自由文本 prompt：

- 明确候选节点说明
- 明确最近一次拒绝原因
- 明确规划能力是否开启
- 明确 function schema 约束

### 8.4 把存储切到 Postgres + Redis

默认 `memory` 只适合本地开发。  
线上至少应该切到：

- Postgres 存快照、任务、摘要、血缘
- Redis 做短期记忆和缓存

---

## 9. 一套推荐的接入流程

如果你今天要把它接进自己的项目，我建议按这个顺序来。

### Phase 1：跑通框架主干

1. 跑 demo
2. 跑 HTTP 服务
3. 调用 `POST /v1/tasks`
4. 看 `summary` 和 `replay`

### Phase 2：接真实模型

1. 配置 `ai.primary`
2. 接通模型网关
3. 保持内置 demo 节点不变
4. 验证真实模型能通过 `route_next_node(...)` 正确路由

### Phase 3：加你的第一个业务节点

1. 先做内置节点
2. 验证 patch 合并
3. 验证准入规则
4. 验证 replay 是否可读

### Phase 4：把高频节点外置化

1. 把需要热更新的节点改成外部 runtime
2. 用 manifest 管理
3. 独立发布和回滚

### Phase 5：上线前切生产后端

1. 切 Postgres
2. 切 Redis
3. 接 OTEL / Prometheus
4. 增加压测和回放排障流程

---

## 10. 当前能力边界

这部分很重要，避免误用。

### 现在已经是“可用底座”的部分

- 动态调度主干
- 节点执行模型
- 状态与快照模型
- 回放与续跑接口
- 外部节点接入模型

### 现在仍然偏“框架初版”的部分

- 内置节点数量少
- 向量记忆还没有完整后端闭环
- 真实 provider 侧的适配还需要按你的模型网关再细化

---

## 11. 最后一句实话

DynAgent 现在最适合的定位是：

**一个可运行、可扩展、可审计、可二开的动态 Agent 执行内核。**

它已经够你做二次开发，但还不应该被误解成“已经封装完所有业务能力的最终平台”。
