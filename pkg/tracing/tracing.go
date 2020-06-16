package tracing

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/AliyunContainerService/terway/types"
)

const (
	// ResourceTypeNetworkService represents resource of a network service
	ResourceTypeNetworkService = "network_service"
	// ResourceTypeResourcePool represents resource of a resource pool(object pool)
	ResourceTypeResourcePool = "resource_pool"
	// ResourceTypeFactory represents resource of a factory(eniip/eni)
	ResourceTypeFactory = "factory"
)

// PodResourceMapping shows the mapping from pod to ResourceMapping
type PodResourceMapping struct {
	Valid    bool
	ResID    string
	PodName  string
	Resource ResourceMapping
}

// FactoryResourceMapping actually get resources from aliyun api
type FactoryResourceMapping struct {
	// ResID: mac / mac:ip
	ResID string
	ENI   *types.ENI
	ENIIP *types.ENIIP
}

// ResourceMapping shows the mapping from resource in the pool to FactoryResource
type ResourceMapping struct {
	Valid           bool
	ResID           string
	ENI             *types.ENI
	IP              net.IP
	FactoryResource FactoryResourceMapping
}

var (
	defaultTracer Tracer
)

// MapKeyValueEntry uses for a in-order key-value store
type MapKeyValueEntry struct {
	Key   string
	Value string
}

// TraceHandler declares functions should be implemented in a tracing component
type TraceHandler interface {
	// Config() returns the static resource config (like min_idle, max_idle, etc) as []MapKeyValueEntry
	Config() []MapKeyValueEntry
	// Trace() returns the trace info (like ENIs count, MAC address) as []MapKeyValueEntry
	Trace() []MapKeyValueEntry
	// Execute(string, []string) execute command in the registered resource, and returns a string channel as stream
	// if the execution has done, the channel should be closed
	Execute(cmd string, args []string, message chan<- string)
}

// ResourceMappingHandler get resource mapping
type ResourceMappingHandler interface {
	GetResourceMapping() ([]PodResourceMapping, error)
}

type resourceMap map[string]TraceHandler

// Tracer manages tracing handlers registered from the system
type Tracer struct {
	// use a RWMutex?
	mtx sync.Mutex
	// store TraceHandler by resource name
	traceMap        map[string]resourceMap
	resourceMapping ResourceMappingHandler
}

func init() {
	defaultTracer.traceMap = make(map[string]resourceMap)
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

// RegisterResourceMapping registers handler to the tracer
func (t *Tracer) RegisterResourceMapping(mapping ResourceMappingHandler) {
	t.resourceMapping = mapping
}

// GetTypes gets all types registered to the tracer
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

// GetConfig invokes Config() function of the given type & resource name
func (t *Tracer) GetConfig(typ, resourceName string) ([]MapKeyValueEntry, error) {
	handler, err := t.getHandler(typ, resourceName)
	if err != nil {
		return nil, err
	}

	return handler.Config(), nil
}

// GetTrace invokes Trace() function of the given type & resource name
func (t *Tracer) GetTrace(typ, resourceName string) ([]MapKeyValueEntry, error) {
	handler, err := t.getHandler(typ, resourceName)
	if err != nil {
		return nil, err
	}

	return handler.Trace(), nil
}

// Execute invokes Execute() function of the given type & resource name with command and arguments
func (t *Tracer) Execute(typ, resourceName, cmd string, args []string) (<-chan string, error) {
	handler, err := t.getHandler(typ, resourceName)
	if err != nil {
		return nil, err
	}

	ch := make(chan string)

	go handler.Execute(cmd, args, ch)
	return ch, nil
}

// GetResourceMapping gives the resource mapping from the handler
// if the handler has not been registered, there will be error
func (t *Tracer) GetResourceMapping() ([]PodResourceMapping, error) {
	if t.resourceMapping == nil {
		return nil, errors.New("no resource mapping handler registered")
	}

	return t.resourceMapping.GetResourceMapping()
}

// Register registers a TraceHandler to the default tracer
func Register(typ, resourceName string, handler TraceHandler) error {
	return defaultTracer.Register(typ, resourceName, handler)
}

// RegisterResourceMapping register resource mapping handler to the default tracer
func RegisterResourceMapping(handler ResourceMappingHandler) {
	defaultTracer.RegisterResourceMapping(handler)
}

// Unregister removes TraceHandler from tracer. do nothing if not found
func Unregister(typ, resourceName string) {
	defaultTracer.Unregister(typ, resourceName)
}

// NewTracer creates a new tracer
func NewTracer() *Tracer {
	return &Tracer{
		mtx:      sync.Mutex{},
		traceMap: make(map[string]resourceMap),
	}
}
