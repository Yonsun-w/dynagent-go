package engine

import (
	"context"
	"testing"
	"time"

	"github.com/admin/ai_project/internal/ai"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/memory"
	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/observe"
	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/rules"
	"github.com/admin/ai_project/internal/sandbox"
	"github.com/admin/ai_project/internal/state"
	"github.com/admin/ai_project/internal/summary"
	"github.com/admin/ai_project/plugins/builtin"
)

func TestEngineRunCompletesWithBuiltins(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{}
	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		t.Fatalf("ApplyDefaultsAndValidate() error = %v", err)
	}
	obs, err := observe.New(ctx, cfg)
	if err != nil {
		t.Fatalf("observe.New() error = %v", err)
	}
	registry, err := node.NewRegistry(config.NodesConfig{
		ManifestDir:     t.TempDir(),
		GRPCDialTimeout: time.Second,
	}, obs.Logger)
	if err != nil {
		t.Fatalf("node.NewRegistry() error = %v", err)
	}
	t.Cleanup(func() {
		_ = registry.Close()
		_ = obs.Shutdown(context.Background())
	})
	if err := builtin.RegisterAll(registry); err != nil {
		t.Fatalf("builtin.RegisterAll() error = %v", err)
	}
	rulesEval, err := rules.NewEvaluator()
	if err != nil {
		t.Fatalf("rules.NewEvaluator() error = %v", err)
	}
	store := persistence.NewMemoryStore()
	engine := New(
		config.ExecutionConfig{
			MaxSteps:          4,
			TaskTimeout:       2 * time.Second,
			NodeTimeout:       time.Second,
			MaxParallelNodes:  2,
			LoopWindow:        3,
			MaxSameNodeVisits: 2,
		},
		obs.Logger,
		obs,
		registry,
		sandbox.New(2, time.Second),
		ai.NewGateway(cfg.AI, obs.Logger),
		rulesEval,
		memory.New(store),
		store,
		summary.New(),
		[]string{"token"},
	)
	st, err := state.New("task-1", "trace-1", state.UserInput{
		Text:     "Summarize this framework execution path.",
		Keywords: []string{"summarize", "framework"},
		Ext:      map[string]any{},
	}, map[string]string{"suite": "engine"})
	if err != nil {
		t.Fatalf("state.New() error = %v", err)
	}
	payload, err := engine.Run(ctx, st)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if payload["final_conclusion"] == "" {
		t.Fatalf("expected final_conclusion in summary payload")
	}
	record, err := store.GetTask(ctx, st.Task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if len(record.Steps) == 0 {
		t.Fatalf("expected persisted steps")
	}
}
