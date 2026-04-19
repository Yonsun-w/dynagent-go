package app

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/admin/ai_project/internal/ai"
	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/engine"
	"github.com/admin/ai_project/internal/memory"
	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/observe"
	"github.com/admin/ai_project/internal/persistence"
	"github.com/admin/ai_project/internal/rules"
	"github.com/admin/ai_project/internal/sandbox"
	"github.com/admin/ai_project/internal/summary"
)

type App struct {
	Config   config.Config
	Observe  *observe.Observability
	Registry *node.Registry
	Store    persistence.Store
	Engine   *engine.Engine
	cleanup  func()
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	if err := os.MkdirAll(cfg.Storage.ColdDataDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure cold data dir: %w", err)
	}
	obs, err := observe.New(ctx, cfg)
	if err != nil {
		return nil, err
	}
	registry, err := node.NewRegistry(cfg.Nodes, obs.Logger)
	if err != nil {
		return nil, err
	}
	store, cleanup, err := persistence.NewStore(ctx, cfg.Storage)
	if err != nil {
		return nil, err
	}
	memoryEngine := memory.New(store)
	rulesEval, err := rules.NewEvaluator()
	if err != nil {
		return nil, err
	}
	engineInstance := engine.New(
		cfg.Execution,
		cfg.AI.RoutingMode,
		obs.Logger,
		obs,
		registry,
		sandbox.New(int64(cfg.Execution.MaxParallelNodes), cfg.Execution.NodeTimeout),
		ai.NewGateway(cfg.AI, obs.Logger),
		rulesEval,
		memoryEngine,
		store,
		summary.New(),
		cfg.Security.SensitiveFields,
	)
	return &App{
		Config:   cfg,
		Observe:  obs,
		Registry: registry,
		Store:    store,
		Engine:   engineInstance,
		cleanup:  cleanup,
	}, nil
}

func (a *App) Logger() *zap.Logger {
	return a.Observe.Logger
}

func (a *App) Close(ctx context.Context) error {
	if a.Registry != nil {
		_ = a.Registry.Close()
	}
	if a.cleanup != nil {
		a.cleanup()
	}
	if a.Observe != nil {
		return a.Observe.Shutdown(ctx)
	}
	return nil
}
