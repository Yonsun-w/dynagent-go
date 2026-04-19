package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/state"
)

type genericHTTPCallNode struct{}

func (genericHTTPCallNode) Meta() node.Meta {
	return node.Meta{
		ID:          "generic_http_call",
		Version:     "v1",
		Description: "Issue a generic GET request to a configured endpoint and store the response body.",
		Labels:      []string{"builtin", "http"},
		InputSchema: node.Schema{Required: []string{}},
		OutputSchema: node.Schema{Required: []string{"performed"}},
	}
}

func (genericHTTPCallNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	snapshot := st.Snapshot()
	if _, ok := snapshot.WorkingMemory["http_url"]; !ok {
		return node.CheckResult{Allowed: false, Reason: "working_memory.http_url is not set"}
	}
	return node.CheckResult{Allowed: true}
}

func (genericHTTPCallNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	snapshot := st.Snapshot()
	url, _ := snapshot.WorkingMemory["http_url"].(string)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return node.Result{Success: false, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return node.Result{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	payload := map[string]any{
		"status_code": resp.StatusCode,
		"performed":   true,
	}
	return node.Result{
		Success: true,
		Output:  payload,
		Patch: state.Patch{
			NodeOutputs: map[string]map[string]any{
				"generic_http_call": payload,
			},
			WorkingMemory: map[string]any{
				"http_result": mustJSON(payload),
			},
		},
	}
}

func mustJSON(input map[string]any) string {
	raw, err := json.Marshal(input)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
