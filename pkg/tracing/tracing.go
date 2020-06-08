package tracing

import (
	"fmt"
	"sync"
)

const (
	ResourceTypeDaemon          = "daemon"
	ResourceTypeResourceDB      = "resource_db"
	ResourceTypeResourceManager = "resource_manager"
	ResourceTypeNetworkService  = "network_service"
	ResourceTypeObjectPool      = "object_pool"
	ResourceTypeFactory         = "factory"
)

var (
	defaultTracer Tracer
)

// TraceHandler
type TraceHandler interface {
	// Config() returns the static resource config (like min_idle, max_idle, etc) as map[string]string
	Config() map[string]string
	// Trace() returns the trace info (like ENIs count, MAC address) as map[string]string
	Trace() map[string]string
}

type resourceMap map[string]TraceHandler

type Tracer struct {
	// use a RWMutex?
	mtx sync.Mutex
	// store TraceHandler by resource name
	traceMap map[string]resourceMap
}

// Register registers a TraceHandler to the tracer
func (t *Tracer) Register(typ, resourceName string, handler TraceHandler) error {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	_, ok := t.traceMap[typ]
	if !ok { // handler of this type not existed before
		t.traceMap[typ] = make(resourceMap)
	}

	_, ok = t.traceMap[typ][resourceName]
	if ok {
		return fmt.Errorf("resource name %s with type %s has been registered", resourceName, typ)
	}

	t.traceMap[typ][resourceName] = handler
	return nil
}

// Unregister remove TraceHandler from tracer. do nothing if not found
func (t *Tracer) Unregister(typ, resourceName string) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	resourceMap, ok := t.traceMap[typ]
	if !ok {
		return
	}

	delete(resourceMap, resourceName)
}

func (t *Tracer) GetTypes() []string {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	var names []string
	// may be unordered, do we need a sort?
	for k := range t.traceMap {
		names = append(names, k)
	}

	return names
}

// GetResourceNames lists resource names of a certain type
func (t *Tracer) GetResourceNames(typ string) []string {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	resourceMap, ok := t.traceMap[typ]
	if !ok {
		// if type not found, return empty array
		return []string{}
	}

	var names []string
	for k := range resourceMap {
		names = append(names, k)
	}

	return names
}

func (t *Tracer) getHandler(typ, resourceName string) (TraceHandler, error) {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	resourceMap, ok := t.traceMap[typ]
	if !ok {
		return nil, fmt.Errorf("tracer type %s not found", typ)
	}

	v, ok := resourceMap[resourceName]
	if !ok {
		return nil, fmt.Errorf("tracer name %s of type %s not found", resourceName, typ)
	}

	return v, nil
}

func (t *Tracer) GetConfig(typ, resourceName string) (map[string]string, error) {
	handler, err := t.getHandler(typ, resourceName)
	if err != nil {
		return nil, err
	}

	return handler.Config(), nil
}

func (t *Tracer) GetTrace(typ, resourceName string) (map[string]string, error) {
	handler, err := t.getHandler(typ, resourceName)
	if err != nil {
		return nil, err
	}

	return handler.Trace(), nil
}

// Register registers a TraceHandler to the tracer
func Register(typ, resourceName string, handler TraceHandler) error {
	return defaultTracer.Register(typ, resourceName, handler)
}

// Unregister remove TraceHandler from tracer. do nothing if not found
func Unregister(typ, resourceName string) {
	defaultTracer.Unregister(typ, resourceName)
}
