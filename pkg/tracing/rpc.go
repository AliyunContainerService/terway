package tracing

import (
	"context"

	"github.com/AliyunContainerService/terway/rpc"
)

type tracingRPC struct {
	tracer *Tracer

	rpc.UnimplementedTerwayTracingServer
}

var _ rpc.TerwayTracingServer = (*tracingRPC)(nil)

// DefaultRPCServer returns the RPC server for default tracer
func DefaultRPCServer() rpc.TerwayTracingServer {
	return RPCServer(&defaultTracer)
}

// RPCServer returns RPC server for the given tracer
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
	response := &rpc.ResourcesNamesReply{ResourceNames: names}

	return response, nil
}

func (t *tracingRPC) GetResourceConfig(_ context.Context, request *rpc.ResourceTypeNameRequest) (*rpc.ResourceConfigReply, error) {
	config, err := t.tracer.GetConfig(request.Type, request.Name)
	if err != nil {
		return nil, err
	}

	var entry []*rpc.MapKeyValueEntry
	for _, v := range config {
		entry = append(entry, toRPCEntry(v))
	}

	response := &rpc.ResourceConfigReply{Config: entry}
	return response, nil
}

func (t *tracingRPC) GetResourceTrace(_ context.Context, request *rpc.ResourceTypeNameRequest) (*rpc.ResourceTraceReply, error) {
	trace, err := t.tracer.GetTrace(request.Type, request.Name)
	if err != nil {
		return nil, err
	}

	var entry []*rpc.MapKeyValueEntry
	for _, v := range trace {
		entry = append(entry, toRPCEntry(v))
	}

	response := &rpc.ResourceTraceReply{Trace: entry}
	return response, nil
}

func (t *tracingRPC) ResourceExecute(request *rpc.ResourceExecuteRequest, server rpc.TerwayTracing_ResourceExecuteServer) error {
	c, err := t.tracer.Execute(request.Type, request.Name, request.Command, request.Args)
	if err != nil {
		return err
	}

	for message := range c {
		if err == nil {
			err = server.Send(&rpc.ResourceExecuteReply{Message: message})
		}
	}

	return nil
}

func (t *tracingRPC) GetResourceMapping(_ context.Context, _ *rpc.Placeholder) (*rpc.PodResourceMappingReply, error) {
	mapping, err := t.tracer.GetResourceMapping()
	if err != nil {
		return nil, err
	}

	var info []*rpc.PodResourceMapping
	for _, m := range mapping {
		info = append(info, toRPCMapping(*m))
	}

	return &rpc.PodResourceMappingReply{
		Info: info,
	}, nil
}

func toRPCMapping(res PodMapping) *rpc.PodResourceMapping {
	rMapping := rpc.PodResourceMapping{
		Type:                rpc.ResourceMappingType_MappingTypeNormal,
		PodName:             res.Name,
		ResourceName:        res.LocalResID,
		FactoryResourceName: res.RemoteResID,
	}
	if !res.Valid {
		rMapping.Type = rpc.ResourceMappingType_MappingTypeError
	}

	return &rMapping
}

func toRPCEntry(entry MapKeyValueEntry) *rpc.MapKeyValueEntry {
	return &rpc.MapKeyValueEntry{
		Key:   entry.Key,
		Value: entry.Value,
	}
}
