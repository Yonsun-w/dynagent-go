package observe

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/admin/ai_project/internal/config"
)

type Observability struct {
	Logger          *zap.Logger
	TracerProvider  *sdktrace.TracerProvider
	TaskCounter     prometheus.Counter
	TaskFailures    prometheus.Counter
	AICallCounter   *prometheus.CounterVec
	NodeLatency     *prometheus.HistogramVec
	NodeOutcome     *prometheus.CounterVec
	MetricsRegistry *prometheus.Registry
}

func New(ctx context.Context, cfg config.Config) (*Observability, error) {
	level := zapcore.InfoLevel
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Observability.LogLevel))); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}
	loggerCfg := zap.NewProductionConfig()
	loggerCfg.Level = zap.NewAtomicLevelAt(level)
	logger, err := loggerCfg.Build()
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}

	registry := prometheus.NewRegistry()
	taskCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dynagent_tasks_total",
		Help: "Total count of accepted tasks.",
	})
	taskFailures := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "dynagent_task_failures_total",
		Help: "Total count of failed tasks.",
	})
	aiCalls := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dynagent_ai_calls_total",
		Help: "Count of AI provider calls partitioned by provider, model and outcome.",
	}, []string{"provider", "model", "outcome"})
	nodeLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dynagent_node_duration_seconds",
		Help:    "Node execution latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"node_id"})
	nodeOutcome := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dynagent_node_outcomes_total",
		Help: "Node execution outcomes partitioned by node and status.",
	}, []string{"node_id", "status"})
	registry.MustRegister(taskCounter, taskFailures, aiCalls, nodeLatency, nodeOutcome)

	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	if endpoint := strings.TrimSpace(cfg.Observability.OTLPEndpoint); endpoint != "" {
		exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("create otlp exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(resource.NewWithAttributes(
				semconv.SchemaURL,
				semconv.ServiceNameKey.String(cfg.Service.Name),
				semconv.DeploymentEnvironmentKey.String(cfg.Service.Env),
			)),
		)
	}
	otel.SetTracerProvider(tp)

	return &Observability{
		Logger:          logger,
		TracerProvider:  tp,
		TaskCounter:     taskCounter,
		TaskFailures:    taskFailures,
		AICallCounter:   aiCalls,
		NodeLatency:     nodeLatency,
		NodeOutcome:     nodeOutcome,
		MetricsRegistry: registry,
	}, nil
}

func (o *Observability) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(o.MetricsRegistry, promhttp.HandlerOpts{})
}

func (o *Observability) Shutdown(ctx context.Context) error {
	var errs []error
	if o.Logger != nil {
		if err := o.Logger.Sync(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.TracerProvider != nil {
		if err := o.TracerProvider.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("shutdown observability: %v", errs)
	}
	return nil
}
