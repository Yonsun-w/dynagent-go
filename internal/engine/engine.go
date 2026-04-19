package engine

import (
	"context"
	"errors"
	"fmt"
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
	cfg        config.ExecutionConfig
	logger     *zap.Logger
	metrics    *observe.Observability
	registry   *node.Registry
	sandbox    *sandbox.Executor
	aiGateway  *ai.Gateway
	rules      *rules.Evaluator
	memory     *memory.Engine
	store      persistence.Store
	summaryGen *summary.Generator
	sensitiveFields []string
}

func New(cfg config.ExecutionConfig, logger *zap.Logger, metrics *observe.Observability, registry *node.Registry, sandboxExecutor *sandbox.Executor, aiGateway *ai.Gateway, rulesEval *rules.Evaluator, memoryEngine *memory.Engine, store persistence.Store, summaryGen *summary.Generator, sensitiveFields []string) *Engine {
	return &Engine{
		cfg:        cfg,
		logger:     logger,
		metrics:    metrics,
		registry:   registry,
		sandbox:    sandboxExecutor,
		aiGateway:  aiGateway,
		rules:      rulesEval,
		memory:     memoryEngine,
		store:      store,
		summaryGen: summaryGen,
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
			Prompt:           buildPrompt(st, recommended),
			State:            readonly,
			RecommendedNodes: recommended,
			OutputSchema: map[string]any{
				"next_node": "string",
				"reasoning": "string",
				"data":      "object",
			},
		})
		if err != nil {
			st.Task.Status = state.TaskStatusFailed
			e.metrics.TaskFailures.Inc()
			return nil, fmt.Errorf("ai decide step %d: %w", step, err)
		}
		st.AppendDecision(state.DecisionRecord{
			Step:      step,
			NextNode:  decision.NextNode,
			Reasoning: decision.Reasoning,
			Data:      decision.Data,
			At:        time.Now().UTC(),
		})
		if decision.NextNode == terminateNode {
			completedByTerminate = true
			break
		}
		visited[decision.NextNode]++
		if visited[decision.NextNode] > e.cfg.MaxSameNodeVisits {
			terminationReason = "loop_detected"
			break
		}

		entry, ok := e.registry.Get(decision.NextNode)
		if !ok {
			return nil, fmt.Errorf("unknown node %q", decision.NextNode)
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
		e.metrics.NodeLatency.WithLabelValues(decision.NextNode).Observe(durationSeconds)
		if err != nil || !result.Success {
			e.metrics.NodeOutcome.WithLabelValues(decision.NextNode, "failed").Inc()
			return nil, fmt.Errorf("execute node %s: %w", decision.NextNode, firstNonNil(err, result.Error))
		}
		e.metrics.NodeOutcome.WithLabelValues(decision.NextNode, "success").Inc()

		snapshot, err := st.ApplyPatch(decision.NextNode, result.Patch, e.sensitiveFields)
		if err != nil {
			return nil, fmt.Errorf("apply patch for node %s: %w", decision.NextNode, err)
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
			NodeID:     decision.NextNode,
			Status:     "success",
			Reasoning:  decision.Reasoning,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			Input:      readonlyInput,
			Output:     result.Output,
		}); err != nil {
			return nil, err
		}
		if err := e.memory.RecordStep(runCtx, st, decision.NextNode); err != nil {
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

func buildPrompt(st *state.State, recommended []string) string {
	return fmt.Sprintf("Task=%s; UserInput=%s; RecommendedNodes=%v; LastRejection=%v", st.Task.ID, st.UserInput.Text, recommended, st.Ext["last_rejection_reason"])
}

func firstNonNil(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}
