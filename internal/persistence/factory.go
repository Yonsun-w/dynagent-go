package persistence

import (
	"context"
	"fmt"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/state"
)

type CompositeStore struct {
	primary Store
	cache   *RedisCache
}

func NewStore(ctx context.Context, cfg config.StorageConfig) (Store, func(), error) {
	switch cfg.Backend {
	case "postgres":
		pg, err := NewPostgresStore(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		cache := NewRedisCache(cfg)
		cleanup := func() {
			_ = cache.Close()
			pg.Close()
		}
		return &CompositeStore{primary: pg, cache: cache}, cleanup, nil
	case "memory":
		return NewMemoryStore(), func() {}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
}

func (c *CompositeStore) CreateTask(ctx context.Context, st state.State) error {
	return c.primary.CreateTask(ctx, st)
}

func (c *CompositeStore) UpdateTask(ctx context.Context, st state.State) error {
	return c.primary.UpdateTask(ctx, st)
}

func (c *CompositeStore) SaveStep(ctx context.Context, taskID string, step StepRecord) error {
	return c.primary.SaveStep(ctx, taskID, step)
}

func (c *CompositeStore) SaveSnapshot(ctx context.Context, snapshot state.Snapshot) error {
	return c.primary.SaveSnapshot(ctx, snapshot)
}

func (c *CompositeStore) SaveSummary(ctx context.Context, taskID string, summary map[string]any) error {
	return c.primary.SaveSummary(ctx, taskID, summary)
}

func (c *CompositeStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	return c.primary.GetTask(ctx, taskID)
}

func (c *CompositeStore) GetLatestSnapshot(ctx context.Context, taskID string) (state.Snapshot, error) {
	return c.primary.GetLatestSnapshot(ctx, taskID)
}

func (c *CompositeStore) PutShortTerm(ctx context.Context, taskID string, nodes []string) error {
	if c.cache != nil {
		return c.cache.PutShortTerm(ctx, taskID, nodes)
	}
	return c.primary.PutShortTerm(ctx, taskID, nodes)
}

func (c *CompositeStore) UpsertPattern(ctx context.Context, pattern Pattern) error {
	return c.primary.UpsertPattern(ctx, pattern)
}

func (c *CompositeStore) RecallPatterns(ctx context.Context, keywords []string) ([]Pattern, error) {
	return c.primary.RecallPatterns(ctx, keywords)
}
