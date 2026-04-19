package contracts

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

const JSONCodecName = "json"

type jsonCodec struct{}

func (jsonCodec) Name() string {
	return JSONCodecName
}

func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func init() {
	encoding.RegisterCodec(jsonCodec{})
}

func JSONCodec() encoding.Codec {
	return jsonCodec{}
}

type Empty struct{}

type MetaResponse struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

type CheckRequest struct {
	TraceID string         `json:"trace_id"`
	State   map[string]any `json:"state"`
}

type CheckResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type ExecuteRequest struct {
	TraceID string         `json:"trace_id"`
	State   map[string]any `json:"state"`
}

type ExecuteResponse struct {
	Success bool           `json:"success"`
	Error   string         `json:"error"`
	Result  map[string]any `json:"result"`
}

type NodeRuntimeServer interface {
	GetMeta(context.Context, *Empty) (*MetaResponse, error)
	Check(context.Context, *CheckRequest) (*CheckResponse, error)
	Execute(context.Context, *ExecuteRequest) (*ExecuteResponse, error)
}

type NodeRuntimeClient interface {
	GetMeta(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*MetaResponse, error)
	Check(ctx context.Context, in *CheckRequest, opts ...grpc.CallOption) (*CheckResponse, error)
	Execute(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*ExecuteResponse, error)
}

type nodeRuntimeClient struct {
	cc grpc.ClientConnInterface
}

func NewNodeRuntimeClient(cc grpc.ClientConnInterface) NodeRuntimeClient {
	return &nodeRuntimeClient{cc: cc}
}

func (c *nodeRuntimeClient) GetMeta(ctx context.Context, in *Empty, opts ...grpc.CallOption) (*MetaResponse, error) {
	out := new(MetaResponse)
	err := c.cc.Invoke(ctx, "/dynagent.noderuntime.v1.NodeRuntime/GetMeta", in, out, opts...)
	return out, err
}

func (c *nodeRuntimeClient) Check(ctx context.Context, in *CheckRequest, opts ...grpc.CallOption) (*CheckResponse, error) {
	out := new(CheckResponse)
	err := c.cc.Invoke(ctx, "/dynagent.noderuntime.v1.NodeRuntime/Check", in, out, opts...)
	return out, err
}

func (c *nodeRuntimeClient) Execute(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*ExecuteResponse, error) {
	out := new(ExecuteResponse)
	err := c.cc.Invoke(ctx, "/dynagent.noderuntime.v1.NodeRuntime/Execute", in, out, opts...)
	return out, err
}

func RegisterNodeRuntimeServer(server grpc.ServiceRegistrar, impl NodeRuntimeServer) {
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "dynagent.noderuntime.v1.NodeRuntime",
		HandlerType: (*NodeRuntimeServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "GetMeta",
				Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					in := new(Empty)
					if err := dec(in); err != nil {
						return nil, err
					}
					if interceptor == nil {
						return impl.GetMeta(ctx, in)
					}
					info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/dynagent.noderuntime.v1.NodeRuntime/GetMeta"}
					handler := func(ctx context.Context, req any) (any, error) {
						return impl.GetMeta(ctx, req.(*Empty))
					}
					return interceptor(ctx, in, info, handler)
				},
			},
			{
				MethodName: "Check",
				Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					in := new(CheckRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					if interceptor == nil {
						return impl.Check(ctx, in)
					}
					info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/dynagent.noderuntime.v1.NodeRuntime/Check"}
					handler := func(ctx context.Context, req any) (any, error) {
						return impl.Check(ctx, req.(*CheckRequest))
					}
					return interceptor(ctx, in, info, handler)
				},
			},
			{
				MethodName: "Execute",
				Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
					in := new(ExecuteRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					if interceptor == nil {
						return impl.Execute(ctx, in)
					}
					info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/dynagent.noderuntime.v1.NodeRuntime/Execute"}
					handler := func(ctx context.Context, req any) (any, error) {
						return impl.Execute(ctx, req.(*ExecuteRequest))
					}
					return interceptor(ctx, in, info, handler)
				},
			},
		},
	}, impl)
}
