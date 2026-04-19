package node

import "context"

type TextGenerationRequest struct {
	SystemPrompt string         `json:"system_prompt"`
	UserPrompt   string         `json:"user_prompt"`
	Input        map[string]any `json:"input,omitempty"`
}

type TextGenerationResult struct {
	Text     string         `json:"text"`
	Provider string         `json:"provider"`
	Model    string         `json:"model"`
	Raw      map[string]any `json:"raw,omitempty"`
}

type Runtime interface {
	GenerateText(ctx context.Context, req TextGenerationRequest) (TextGenerationResult, error)
}

type runtimeContextKey struct{}

func WithRuntime(ctx context.Context, runtime Runtime) context.Context {
	if runtime == nil {
		return ctx
	}
	return context.WithValue(ctx, runtimeContextKey{}, runtime)
}

func RuntimeFromContext(ctx context.Context) (Runtime, bool) {
	runtime, ok := ctx.Value(runtimeContextKey{}).(Runtime)
	return runtime, ok
}
