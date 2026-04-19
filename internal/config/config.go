package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Server        ServerConfig        `yaml:"server"`
	Execution     ExecutionConfig     `yaml:"execution"`
	AI            AIConfig            `yaml:"ai"`
	Storage       StorageConfig       `yaml:"storage"`
	Observability ObservabilityConfig `yaml:"observability"`
	Security      SecurityConfig      `yaml:"security"`
	Nodes         NodesConfig         `yaml:"nodes"`
}

type ServiceConfig struct {
	Name string `yaml:"name"`
	Env  string `yaml:"env"`
}

type ServerConfig struct {
	Address         string        `yaml:"address"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

type ExecutionConfig struct {
	MaxSteps         int           `yaml:"max_steps"`
	TaskTimeout      time.Duration `yaml:"task_timeout"`
	NodeTimeout      time.Duration `yaml:"node_timeout"`
	MaxParallelNodes int           `yaml:"max_parallel_nodes"`
	LoopWindow       int           `yaml:"loop_window"`
	MaxSameNodeVisits int          `yaml:"max_same_node_visits"`
}

type ModelConfig struct {
	Provider  string        `yaml:"provider"`
	Endpoint  string        `yaml:"endpoint"`
	Model     string        `yaml:"model"`
	APIKeyEnv string        `yaml:"api_key_env"`
	Timeout   time.Duration `yaml:"timeout"`
}

type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	BaseDelay   time.Duration `yaml:"base_delay"`
}

type RateLimitConfig struct {
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	Burst             int     `yaml:"burst"`
}

type CircuitBreakerConfig struct {
	MaxRequests  uint32        `yaml:"max_requests"`
	Interval     time.Duration `yaml:"interval"`
	Timeout      time.Duration `yaml:"timeout"`
	FailureRatio float64       `yaml:"failure_ratio"`
}

type AIConfig struct {
	Primary        ModelConfig          `yaml:"primary"`
	Fallback       ModelConfig          `yaml:"fallback"`
	Retry          RetryConfig          `yaml:"retry"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker"`
}

type StorageConfig struct {
	Backend      string `yaml:"backend"`
	PostgresDSN  string `yaml:"postgres_dsn"`
	RedisAddr    string `yaml:"redis_addr"`
	RedisPassword string `yaml:"redis_password"`
	RedisDB      int    `yaml:"redis_db"`
	ColdDataDir  string `yaml:"cold_data_dir"`
}

type ObservabilityConfig struct {
	LogLevel    string `yaml:"log_level"`
	MetricsPath string `yaml:"metrics_path"`
	OTLPEndpoint string `yaml:"otlp_endpoint"`
	EnablePprof bool   `yaml:"enable_pprof"`
}

type SecurityConfig struct {
	SensitiveFields []string `yaml:"sensitive_fields"`
	EncryptionKey   string   `yaml:"encryption_key"`
}

type NodesConfig struct {
	ManifestDir     string        `yaml:"manifest_dir"`
	PollInterval    time.Duration `yaml:"poll_interval"`
	GRPCDialTimeout time.Duration `yaml:"grpc_dial_timeout"`
}

func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, errors.New("config path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) ApplyDefaultsAndValidate() error {
	if c.Service.Name == "" {
		c.Service.Name = "dynagent"
	}
	if c.Service.Env == "" {
		c.Service.Env = "local"
	}
	if c.Server.Address == "" {
		c.Server.Address = ":8080"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 10 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 15 * time.Second
	}
	if c.Server.ShutdownTimeout == 0 {
		c.Server.ShutdownTimeout = 15 * time.Second
	}
	if c.Execution.MaxSteps <= 0 {
		c.Execution.MaxSteps = 12
	}
	if c.Execution.TaskTimeout <= 0 {
		c.Execution.TaskTimeout = 90 * time.Second
	}
	if c.Execution.NodeTimeout <= 0 {
		c.Execution.NodeTimeout = 10 * time.Second
	}
	if c.Execution.MaxParallelNodes <= 0 {
		c.Execution.MaxParallelNodes = 16
	}
	if c.Execution.LoopWindow <= 0 {
		c.Execution.LoopWindow = 4
	}
	if c.Execution.MaxSameNodeVisits <= 0 {
		c.Execution.MaxSameNodeVisits = 3
	}
	if c.Observability.LogLevel == "" {
		c.Observability.LogLevel = "info"
	}
	if c.Observability.MetricsPath == "" {
		c.Observability.MetricsPath = "/metrics"
	}
	if c.Storage.Backend == "" {
		c.Storage.Backend = "memory"
	}
	if c.Storage.ColdDataDir == "" {
		c.Storage.ColdDataDir = "./var/cold"
	}
	if c.Nodes.ManifestDir == "" {
		c.Nodes.ManifestDir = "./configs/nodes.d"
	}
	if c.Nodes.PollInterval <= 0 {
		c.Nodes.PollInterval = 3 * time.Second
	}
	if c.Nodes.GRPCDialTimeout <= 0 {
		c.Nodes.GRPCDialTimeout = 3 * time.Second
	}
	if c.AI.Primary.Provider == "" {
		c.AI.Primary.Provider = "mock"
		c.AI.Primary.Model = "mock-router"
	}
	if c.AI.Primary.Timeout <= 0 {
		c.AI.Primary.Timeout = 5 * time.Second
	}
	if c.AI.Fallback.Provider == "" {
		c.AI.Fallback.Provider = "mock"
		c.AI.Fallback.Model = "mock-fallback"
	}
	if c.AI.Fallback.Timeout <= 0 {
		c.AI.Fallback.Timeout = 5 * time.Second
	}
	if c.AI.Retry.MaxAttempts <= 0 {
		c.AI.Retry.MaxAttempts = 2
	}
	if c.AI.Retry.BaseDelay <= 0 {
		c.AI.Retry.BaseDelay = 300 * time.Millisecond
	}
	if c.AI.RateLimit.RequestsPerSecond <= 0 {
		c.AI.RateLimit.RequestsPerSecond = 20
	}
	if c.AI.RateLimit.Burst <= 0 {
		c.AI.RateLimit.Burst = 10
	}
	if c.AI.CircuitBreaker.MaxRequests == 0 {
		c.AI.CircuitBreaker.MaxRequests = 2
	}
	if c.AI.CircuitBreaker.Interval <= 0 {
		c.AI.CircuitBreaker.Interval = 30 * time.Second
	}
	if c.AI.CircuitBreaker.Timeout <= 0 {
		c.AI.CircuitBreaker.Timeout = 20 * time.Second
	}
	if c.AI.CircuitBreaker.FailureRatio <= 0 {
		c.AI.CircuitBreaker.FailureRatio = 0.6
	}
	if c.Nodes.ManifestDir != "" {
		c.Nodes.ManifestDir = filepath.Clean(c.Nodes.ManifestDir)
	}
	if c.Storage.ColdDataDir != "" {
		c.Storage.ColdDataDir = filepath.Clean(c.Storage.ColdDataDir)
	}

	if c.Service.Name == "" {
		return errors.New("service.name must not be empty")
	}
	if c.Server.Address == "" {
		return errors.New("server.address must not be empty")
	}
	return nil
}
