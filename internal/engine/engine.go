package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	"github.com/admin/ai_project/internal/ai"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/memory"
	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/observe"
	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/rules"
	"github.com/admin/ai_project/internal/sandbox"
	"github.com/admin/ai_project/internal/state"
	"github.com/admin/ai_project/internal/summary"
)

const terminateNode = "__terminate__"

type Engine struct {
	cfg             config.ExecutionConfig
	routingMode     string
	logger          *zap.Logger
	metrics         *observe.Observability
	registry        *node.Registry
	sandbox         *sandbox.Executor
	aiGateway       *ai.Gateway
	rules           *rules.Evaluator
	memory          *memory.Engine
	store           persistence.Store
	summaryGen      *summary.Generator
	sensitiveFields []string
}

func New(cfg config.ExecutionConfig, routingMode string, logger *zap.Logger, metrics *observe.Observability, registry *node.Registry, sandboxExecutor *sandbox.Executor, aiGateway *ai.Gateway, rulesEval *rules.Evaluator, memoryEngine *memory.Engine, store persistence.Store, summaryGen *summary.Generator, sensitiveFields []string) *Engine {
	return &Engine{
		cfg:             cfg,
		routingMode:     routingMode,
		logger:          logger,
		metrics:         metrics,
		registry:        registry,
		sandbox:         sandboxExecutor,
		aiGateway:       aiGateway,
		rules:           rulesEval,
		memory:          memoryEngine,
		store:           store,
		summaryGen:      summaryGen,
		sensitiveFields: sensitiveFields,
	}
}

func (e *Engine) Run(ctx context.Context, st *state.State) (map[string]any, error) {
	traceID := st.Trace.TraceID
	runCtx, cancel := context.WithTimeout(platform.ContextWithTraceID(ctx, traceID), e.cfg.TaskTimeout)
	defer cancel()

	if _, err := e.store.GetTask(runCtx, st.Task.ID); err != nil {
		if err := e.store.CreateTask(runCtx, *st); err != nil {
			return nil, err
		}
		e.metrics.TaskCounter.Inc()
	} else {
		if err := e.store.UpdateTask(runCtx, *st); err != nil {
			return nil, err
		}
	}

	visited := map[string]int{}
	terminationReason := "ai_terminate"
	completedByTerminate := false
	for step := 1; step <= e.cfg.MaxSteps; step++ {
		recommended, err := e.memory.RecommendNodes(runCtx, st)
		if err != nil {
			e.logger.Warn("recommend nodes failed", zap.Error(err))
		}
		readonly, err := st.ReadOnly()
		if err != nil {
			return nil, err
		}
		decision, err := e.aiGateway.Decide(runCtx, ai.Request{
			Context: ai.RoutingContext{
				TaskID:              st.Task.ID,
				UserInput:           st.UserInput.Text,
				Keywords:            st.UserInput.Keywords,
				State:               readonly,
				CandidateNodes:      buildCandidateNodes(e.registry.List()),
				RecommendedNodes:    recommended,
				LastRejectionReason: fmt.Sprint(st.Ext["last_rejection_reason"]),
				PlanningEnabled:     e.routingMode == "route_and_plan",
			},
		})
		if err != nil {
			st.Task.Status = state.TaskStatusFailed
			e.metrics.TaskFailures.Inc()
			return nil, fmt.Errorf("ai decide step %d: %w", step, err)
		}

		switch decision.Type {
		case ai.DecisionTypePlan:
			if decision.Plan == nil {
				return nil, errors.New("planning decision must include a plan payload")
			}
			st.Ext["latest_plan"] = map[string]any{
				"goal":      decision.Plan.Goal,
				"nodes":     decision.Plan.Nodes,
				"edges":     decision.Plan.Edges,
				"reasoning": decision.Plan.Reasoning,
				"data":      decision.Plan.Data,
			}
			st.AppendDecision(state.DecisionRecord{
				DecisionType: string(ai.DecisionTypePlan),
				FunctionName: decision.FunctionCall.Name,
				FunctionArgs: decision.FunctionCall.Arguments,
				Step:         step,
				Reasoning:    decision.Plan.Reasoning,
				Data: map[string]any{
					"goal":  decision.Plan.Goal,
					"nodes": decision.Plan.Nodes,
					"edges": decision.Plan.Edges,
					"data":  decision.Plan.Data,
				},
				At: time.Now().UTC(),
			})
			continue
		case ai.DecisionTypeRoute:
		default:
			return nil, fmt.Errorf("unsupported decision type %q", decision.Type)
		}

		if decision.Route == nil {
			return nil, errors.New("route decision must include a route payload")
		}

		st.AppendDecision(state.DecisionRecord{
			DecisionType: string(ai.DecisionTypeRoute),
			FunctionName: decision.FunctionCall.Name,
			FunctionArgs: decision.FunctionCall.Arguments,
			Step:         step,
			NextNode:     decision.Route.NextNode,
			Reasoning:    decision.Route.Reasoning,
			Data:         decision.Route.Data,
			At:           time.Now().UTC(),
		})
		if decision.Route.NextNode == terminateNode {
			completedByTerminate = true
			break
		}
		visited[decision.Route.NextNode]++
		if visited[decision.Route.NextNode] > e.cfg.MaxSameNodeVisits {
			terminationReason = "loop_detected"
			break
		}

		entry, ok := e.registry.Get(decision.Route.NextNode)
		if !ok {
			return nil, fmt.Errorf("unknown node %q", decision.Route.NextNode)
		}

		if entry.Rules != nil {
			check := e.rules.Evaluate(runCtx, *entry.Rules, readonly)
			if !check.Allowed {
				st.Ext["last_rejection_reason"] = check.Reason
				continue
			}
		}

		nodeCheck := entry.Node.CheckBefore(runCtx, readonly)
		if !nodeCheck.Allowed {
			st.Ext["last_rejection_reason"] = nodeCheck.Reason
			continue
		}

		startedAt := time.Now().UTC()
		result, err := e.sandbox.Execute(runCtx, entry.Node, readonly)
		finishedAt := time.Now().UTC()
		durationSeconds := finishedAt.Sub(startedAt).Seconds()
		e.metrics.NodeLatency.WithLabelValues(decision.Route.NextNode).Observe(durationSeconds)
		if err != nil || !result.Success {
			e.metrics.NodeOutcome.WithLabelValues(decision.Route.NextNode, "failed").Inc()
			return nil, fmt.Errorf("execute node %s: %w", decision.Route.NextNode, firstNonNil(err, result.Error))
		}
		e.metrics.NodeOutcome.WithLabelValues(decision.Route.NextNode, "success").Inc()

		snapshot, err := st.ApplyPatch(decision.Route.NextNode, result.Patch, e.sensitiveFields)
		if err != nil {
			return nil, fmt.Errorf("apply patch for node %s: %w", decision.Route.NextNode, err)
		}
		if err := e.store.UpdateTask(runCtx, *st); err != nil {
			return nil, err
		}
		if err := e.store.SaveSnapshot(runCtx, *snapshot); err != nil {
			return nil, err
		}
		readonlyInput, _ := readonly.ToMap()
		if err := e.store.SaveStep(runCtx, st.Task.ID, persistence.StepRecord{
			StepIndex:  step,
			NodeID:     decision.Route.NextNode,
			Status:     "success",
			Reasoning:  decision.Route.Reasoning,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			Input:      readonlyInput,
			Output:     result.Output,
		}); err != nil {
			return nil, err
		}
		if err := e.memory.RecordStep(runCtx, st, decision.Route.NextNode); err != nil {
			e.logger.Warn("record step memory failed", zap.Error(err))
		}
	}
	if !completedByTerminate && terminationReason == "ai_terminate" {
		terminationReason = "max_steps"
	}

	if err := runCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			terminationReason = "task_timeout"
			st.Task.Status = state.TaskStatusTimedOut
		}
	}
	if terminationReason == "ai_terminate" && st.Task.Status != state.TaskStatusTimedOut {
		st.Task.Status = state.TaskStatusCompleted
	}
	if terminationReason == "loop_detected" || terminationReason == "max_steps" {
		st.Task.Status = state.TaskStatusFailed
	}

	record, err := e.store.GetTask(runCtx, st.Task.ID)
	if err != nil {
		return nil, err
	}
	if err := e.memory.Finalize(runCtx, st); err != nil {
		e.logger.Warn("finalize memory failed", zap.Error(err))
	}
	summaryPayload := e.summaryGen.Build(runCtx, st, record, terminationReason)
	if err := e.store.SaveSummary(runCtx, st.Task.ID, summaryPayload); err != nil {
		return nil, err
	}
	if err := e.store.UpdateTask(runCtx, *st); err != nil {
		return nil, err
	}
	return summaryPayload, nil
}

func firstNonNil(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}

func buildCandidateNodes(metas []node.Meta) []ai.CandidateNode {
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].ID < metas[j].ID
	})
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
