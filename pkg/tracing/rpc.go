package tracing

import (
	"context"
	"github.com/AliyunContainerService/terway/rpc"
)

type tracingRPC struct {
	tracer *Tracer
}

func DefaultRPCServer() rpc.TerwayTracingServer {
	return RPCServer(&defaultTracer)
}

func RPCServer(tracer *Tracer) rpc.TerwayTracingServer {
	return &tracingRPC{tracer: tracer}
}

func (t *tracingRPC) GetResourceTypes(_ context.Context, _ *rpc.Placeholder) (*rpc.ResourcesTypesReply, error) {
	names := t.tracer.GetTypes()
	response := &rpc.ResourcesTypesReply{TypeNames: names}

	return response, nil
}

func (t *tracingRPC) GetResources(_ context.Context, request *rpc.ResourceTypeRequest) (*rpc.ResourcesNamesReply, error) {
	names := t.tracer.GetResourceNames(request.Name)
	response := &rpc.ResourcesNamesReply{Name: names}

	return response, nil
}

func (t *tracingRPC) GetResourceConfig(_ context.Context, request *rpc.ResourceTypeNameRequest) (*rpc.ResourceConfigReply, error) {
	config, err := t.tracer.GetConfig(request.Type, request.Name)
	if err != nil {
		return nil, err
	}

	response := &rpc.ResourceConfigReply{Config: config}
	return response, nil
}

func (t *tracingRPC) GetResourceTrace(_ context.Context, request *rpc.ResourceTypeNameRequest) (*rpc.ResourceTraceReply, error) {
	trace, err := t.tracer.GetTrace(request.Type, request.Name)
	if err != nil {
		return nil, err
	}

	response := &rpc.ResourceTraceReply{Trace: trace}
	return response, nil
}
