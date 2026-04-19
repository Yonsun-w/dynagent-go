package config

import "testing"

func TestValidateAIConfig_AllowsVendorProviders(t *testing.T) {
	t.Parallel()

	cfg := AIConfig{
		RoutingMode: "route_only",
		Primary: ModelConfig{
			Provider: "openai",
		},
		Fallback: ModelConfig{
			Provider: "qwen",
		},
	}
	if err := validateAIConfig(cfg); err != nil {
		t.Fatalf("validateAIConfig returned error: %v", err)
	}

	for _, provider := range []string{"kimi", "glm", "anthropic", "mock_function", "openai_compatible"} {
		cfg.Fallback.Provider = provider
		if err := validateAIConfig(cfg); err != nil {
			t.Fatalf("validateAIConfig(%q) returned error: %v", provider, err)
		}
	}
}
