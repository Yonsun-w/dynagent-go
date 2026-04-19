package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"

	"google.golang.org/grpc"

	"github.com/admin/ai_project/internal/node"
	"github.com/admin/ai_project/internal/platform"
	"github.com/admin/ai_project/internal/state"
	"github.com/admin/ai_project/pkg/contracts"
)

type runtimeServer struct {
	node node.Node
}

func (s runtimeServer) GetMeta(ctx context.Context, _ *contracts.Empty) (*contracts.MetaResponse, error) {
	_ = ctx
	meta := s.node.Meta()
	return &contracts.MetaResponse{
		ID:          meta.ID,
		Version:     meta.Version,
		Description: meta.Description,
	}, nil
}

func (s runtimeServer) Check(ctx context.Context, req *contracts.CheckRequest) (*contracts.CheckResponse, error) {
	st, err := state.FromMap(req.State)
	if err != nil {
		return nil, err
	}
	readonly, err := st.ReadOnly()
	if err != nil {
		return nil, err
	}
	result := s.node.CheckBefore(platform.ContextWithTraceID(ctx, req.TraceID), readonly)
	return &contracts.CheckResponse{Allowed: result.Allowed, Reason: result.Reason}, nil
}

func (s runtimeServer) Execute(ctx context.Context, req *contracts.ExecuteRequest) (*contracts.ExecuteResponse, error) {
	st, err := state.FromMap(req.State)
	if err != nil {
		return nil, err
	}
	readonly, err := st.ReadOnly()
	if err != nil {
		return nil, err
	}
	result := s.node.Execute(platform.ContextWithTraceID(ctx, req.TraceID), readonly)
	return &contracts.ExecuteResponse{
		Success: result.Success,
		Error:   result.Error,
		Result: map[string]any{
			"success": result.Success,
			"output":  result.Output,
			"patch":   result.Patch,
			"error":   result.Error,
			"meta":    result.Meta,
		},
	}, nil
}

type externalEchoNode struct{}

func (externalEchoNode) Meta() node.Meta {
	return node.Meta{
		ID:          "external_echo",
		Version:     "v1",
		Description: "External node example that echoes the user text.",
		Labels:      []string{"external", "demo"},
		OutputSchema: node.Schema{Required: []string{"echoed_text"}},
	}
}

func (externalEchoNode) CheckBefore(ctx context.Context, st *state.ReadOnlyState) node.CheckResult {
	_ = ctx
	if st.UserText() == "" {
		return node.CheckResult{Allowed: false, Reason: "empty user text"}
	}
	return node.CheckResult{Allowed: true}
}

func (externalEchoNode) Execute(ctx context.Context, st *state.ReadOnlyState) node.Result {
	_ = ctx
	echoed := st.UserText()
	return node.Result{
		Success: true,
		Output:  map[string]any{"echoed_text": echoed},
		Patch: state.Patch{
			WorkingMemory: map[string]any{"echoed_text": echoed},
			NodeOutputs: map[string]map[string]any{
				"external_echo": {"echoed_text": echoed},
			},
		},
	}
}

func main() {
	var manifestPath string
	flag.StringVar(&manifestPath, "manifest", "", "optional manifest path for future extensions")
	flag.Parse()
	_ = manifestPath

	address := os.Getenv("DYNAGENT_NODE_ADDRESS")
	if address == "" {
		address = "127.0.0.1:9091"
	}
	handler := os.Getenv("DYNAGENT_NODE_HANDLER")
	if handler == "" {
		handler = "external_echo"
	}

	var n node.Node
	switch handler {
	case "external_echo":
		n = externalEchoNode{}
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown external handler %q\n", handler)
		os.Exit(1)
	}

	lis, err := net.Listen("tcp", address)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "listen on %s: %v\n", address, err)
		os.Exit(1)
	}
	server := grpc.NewServer()
	contracts.RegisterNodeRuntimeServer(server, runtimeServer{node: n})
	if err := server.Serve(lis); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "serve node runtime: %v\n", err)
		os.Exit(1)
	}
}
