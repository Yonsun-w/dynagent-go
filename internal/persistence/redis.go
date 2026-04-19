package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/admin/ai_project/internal/config"
)

type RedisCache struct {
	client *redis.Client
}

func NewRedisCache(cfg config.StorageConfig) *RedisCache {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	return &RedisCache{client: client}
}

func (r *RedisCache) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

func (r *RedisCache) PutShortTerm(ctx context.Context, taskID string, nodes []string) error {
	raw, err := json.Marshal(nodes)
	if err != nil {
		return fmt.Errorf("marshal short term trajectory: %w", err)
	}
	return r.client.Set(ctx, "dynagent:short:"+taskID, raw, 24*time.Hour).Err()
}
