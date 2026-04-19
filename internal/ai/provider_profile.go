package ai

import (
	"fmt"
	"os"
	"strings"

	"github.com/admin/ai_project/internal/config"
)

type ProviderProfile struct {
	Name          string `json:"name"`
	Family        string `json:"family"`
	ToolProtocol  string `json:"tool_protocol"`
	Endpoint      string `json:"endpoint"`
	Model         string `json:"model"`
	APIKeyEnv     string `json:"api_key_env"`
	APIKeyPresent bool   `json:"api_key_present"`
}

func DescribeProvider(cfg config.ModelConfig) ProviderProfile {
	provider := normalizeProviderName(cfg.Provider)
	return ProviderProfile{
		Name:          provider,
		Family:        providerFamily(provider),
		ToolProtocol:  providerToolProtocol(provider),
		Endpoint:      strings.TrimSpace(cfg.Endpoint),
		Model:         strings.TrimSpace(cfg.Model),
		APIKeyEnv:     strings.TrimSpace(cfg.APIKeyEnv),
		APIKeyPresent: strings.TrimSpace(cfg.APIKeyEnv) != "" && strings.TrimSpace(os.Getenv(cfg.APIKeyEnv)) != "",
	}
}

func ValidateProviderRuntime(cfg config.ModelConfig) error {
	provider := normalizeProviderName(cfg.Provider)
	if provider == "" {
		return fmt.Errorf("ai provider must not be empty")
	}
	if provider == "mock_function" {
		return nil
	}

	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("provider %q requires a non-empty endpoint", provider)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("provider %q requires a non-empty model", provider)
	}
	if strings.TrimSpace(cfg.APIKeyEnv) == "" {
		return fmt.Errorf("provider %q requires api_key_env", provider)
	}
	if strings.TrimSpace(os.Getenv(cfg.APIKeyEnv)) == "" {
		return fmt.Errorf("provider %q requires environment variable %q", provider, cfg.APIKeyEnv)
	}
	return nil
}

func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func providerFamily(provider string) string {
	switch provider {
	case "openai", "openai_compatible", "qwen", "kimi", "glm":
		return "openai_compatible"
	case "anthropic":
		return "anthropic"
	case "mock_function":
		return "mock_function"
	default:
		return "unknown"
	}
}

func providerToolProtocol(provider string) string {
	switch providerFamily(provider) {
	case "anthropic":
		return "anthropic.tools"
	case "openai_compatible":
		return "openai.tools"
	case "mock_function":
		return "in_memory.function_call"
	default:
		return "unknown"
	}
}
