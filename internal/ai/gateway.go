package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/admin/ai_project/internal/config"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
)

type Decision struct {
	NextNode  string         `json:"next_node"`
	Reasoning string         `json:"reasoning"`
	Data      map[string]any `json:"data"`
}

type Request struct {
	Prompt           string            `json:"prompt"`
	State            *state.ReadOnlyState `json:"state"`
	RecommendedNodes []string          `json:"recommended_nodes"`
	OutputSchema     map[string]any    `json:"output_schema"`
}

type Provider interface {
	Name() string
	Decide(ctx context.Context, cfg config.ModelConfig, req Request) (Decision, int, error)
}

type Gateway struct {
	logger   *zap.Logger
	primary  config.ModelConfig
	fallback config.ModelConfig
	retry    config.RetryConfig
	provider map[string]Provider
	limiter  *rate.Limiter
	breaker  *gobreaker.CircuitBreaker[Decision]
}

func NewGateway(cfg config.AIConfig, logger *zap.Logger) *Gateway {
	breaker := gobreaker.NewCircuitBreaker[Decision](gobreaker.Settings{
		Name:        "ai_gateway",
		MaxRequests: cfg.CircuitBreaker.MaxRequests,
		Interval:    cfg.CircuitBreaker.Interval,
		Timeout:     cfg.CircuitBreaker.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < 5 {
				return false
			}
			return float64(counts.TotalFailures)/float64(counts.Requests) >= cfg.CircuitBreaker.FailureRatio
		},
	})
	return &Gateway{
		logger:   logger,
		primary:  cfg.Primary,
		fallback: cfg.Fallback,
		retry:    cfg.Retry,
		provider: map[string]Provider{
			"mock":       mockProvider{},
			"openai":     httpProvider{kind: "openai"},
			"compatible": httpProvider{kind: "compatible"},
			"anthropic":  anthropicProvider{},
		},
		limiter: rate.NewLimiter(rate.Limit(cfg.RateLimit.RequestsPerSecond), cfg.RateLimit.Burst),
		breaker: breaker,
	}
}

func (g *Gateway) Decide(ctx context.Context, req Request) (Decision, error) {
	if err := g.limiter.Wait(ctx); err != nil {
		return Decision{}, fmt.Errorf("rate limit wait: %w", err)
	}
	return g.breaker.Execute(func() (Decision, error) {
		decision, _, err := g.tryModel(ctx, g.primary, req)
		if err == nil {
			return normalizeDecision(decision)
		}
		g.logger.Warn("primary ai model failed, trying fallback", zap.String("provider", g.primary.Provider), zap.Error(err))
		decision, _, fallbackErr := g.tryModel(ctx, g.fallback, req)
		if fallbackErr != nil {
			return Decision{}, fmt.Errorf("fallback failed after primary error %v: %w", err, fallbackErr)
		}
		return normalizeDecision(decision)
	})
}

func (g *Gateway) tryModel(ctx context.Context, cfg config.ModelConfig, req Request) (Decision, int, error) {
	provider, ok := g.provider[strings.ToLower(cfg.Provider)]
	if !ok {
		return Decision{}, 0, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	var lastErr error
	for attempt := 1; attempt <= g.retry.MaxAttempts; attempt++ {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		childCtx, cancel := context.WithTimeout(ctx, timeout)
		decision, status, err := provider.Decide(childCtx, cfg, req)
		cancel()
		if err == nil {
			return decision, status, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return Decision{}, status, ctx.Err()
		case <-time.After(time.Duration(attempt) * g.retry.BaseDelay):
		}
	}
	return Decision{}, 0, lastErr
}

func normalizeDecision(in Decision) (Decision, error) {
	in.NextNode = strings.TrimSpace(in.NextNode)
	if in.NextNode == "" {
		return Decision{}, errors.New("ai decision next_node must not be empty")
	}
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	return in, nil
}

type mockProvider struct{}

func (mockProvider) Name() string { return "mock" }

func (mockProvider) Decide(ctx context.Context, cfg config.ModelConfig, req Request) (Decision, int, error) {
	_ = ctx
	_ = cfg
	snapshot := req.State.Snapshot()
	if _, ok := snapshot.NodeOutputs["intent_parse"]; !ok {
		return Decision{NextNode: "intent_parse", Reasoning: "Need to parse the incoming user intent.", Data: map[string]any{}}, 200, nil
	}
	if _, ok := snapshot.NodeOutputs["text_transform"]; !ok {
		return Decision{NextNode: "text_transform", Reasoning: "Transform text before finalization.", Data: map[string]any{}}, 200, nil
	}
	if _, ok := snapshot.NodeOutputs["finalize"]; ok {
		return Decision{NextNode: "__terminate__", Reasoning: "Final answer already exists, terminate the execution chain.", Data: map[string]any{}}, 200, nil
	}
	return Decision{NextNode: "finalize", Reasoning: "All prerequisite information is ready.", Data: map[string]any{}}, 200, nil
}

type httpProvider struct {
	kind string
}

func (p httpProvider) Name() string { return p.kind }

func (p httpProvider) Decide(ctx context.Context, cfg config.ModelConfig, req Request) (Decision, int, error) {
	payload, err := buildHTTPPrompt(req)
	if err != nil {
		return Decision{}, 0, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, 0, fmt.Errorf("marshal provider request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return Decision{}, 0, fmt.Errorf("create provider request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cfg.APIKeyEnv != "" {
		httpReq.Header.Set("Authorization", "Bearer "+os.Getenv(cfg.APIKeyEnv))
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return Decision{}, 0, fmt.Errorf("provider request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Decision{}, resp.StatusCode, fmt.Errorf("read provider response: %w", err)
	}
	var decision Decision
	if err := json.Unmarshal(data, &decision); err != nil {
		return Decision{}, resp.StatusCode, fmt.Errorf("parse provider response: %w", err)
	}
	return decision, resp.StatusCode, nil
}

type anthropicProvider struct{}

func (anthropicProvider) Name() string { return "anthropic" }

func (anthropicProvider) Decide(ctx context.Context, cfg config.ModelConfig, req Request) (Decision, int, error) {
	return httpProvider{kind: "anthropic"}.Decide(ctx, cfg, req)
}

func buildHTTPPrompt(req Request) (map[string]any, error) {
	stateMap, err := req.State.ToMap()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"prompt":            req.Prompt,
		"state":             stateMap,
		"recommended_nodes": req.RecommendedNodes,
		"schema":            req.OutputSchema,
		"trace_id":          platform.TraceIDFromContext(context.Background()),
	}, nil
}
