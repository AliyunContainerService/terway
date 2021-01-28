package tracing

import (
	"errors"
	"fmt"
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

	// DisposeResourceFailed DisposeResourceFailed
	DisposeResourceFailed = "DisposeResourceFailed"
	// AllocResourceFailed AllocResourceFailed
	AllocResourceFailed = "AllocResourceFailed"
)

// PodMapping PodMapping
type PodMapping struct {
	Name      string
	Namespace string
	Valid     bool

	PodBindResID string
	LocalResID   string
	RemoteResID  string
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

// ResourcePoolStats define two pool lo and remote
type ResourcePoolStats interface {
	GetLocal() map[string]types.Res
	GetRemote() map[string]types.Res
}

// FakeResourcePoolStats for test
type FakeResourcePoolStats struct {
	Local  map[string]types.Res
	Remote map[string]types.Res
}

// GetLocal GetLocal
func (f *FakeResourcePoolStats) GetLocal() map[string]types.Res {
	return f.Local
}

// GetRemote GetRemote
func (f *FakeResourcePoolStats) GetRemote() map[string]types.Res {
	return f.Remote
}

// ResourceMappingHandler get resource mapping
type ResourceMappingHandler interface {
	GetResourceMapping() (ResourcePoolStats, error)
}

// ResMapping ResMapping
type ResMapping interface {
	GetResourceMapping() ([]*PodMapping, error)
}

// PodEventRecorder records event on pod
type PodEventRecorder func(podName, podNamespace, eventType, reason, message string) error

// NodeEventRecorder records event on node
type NodeEventRecorder func(eventType, reason, message string)

type resourceMap map[string]TraceHandler

// Tracer manages tracing handlers registered from the system
type Tracer struct {
	// use a RWMutex?
	mtx sync.Mutex
	// store TraceHandler by resource name
	traceMap        map[string]resourceMap
	resourceMapping ResMapping
	podEvent        PodEventRecorder
	nodeEvent       NodeEventRecorder
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
func (t *Tracer) RegisterResourceMapping(mapping ResMapping) {
	t.resourceMapping = mapping
}

// RegisterEventRecorder registers pod & node event recorder to a tracer
func (t *Tracer) RegisterEventRecorder(node NodeEventRecorder, pod PodEventRecorder) {
	t.nodeEvent = node
	t.podEvent = pod
}

// GetTypes gets all types registered to the tracer
func (t *Tracer) GetTypes() []string {
	t.mtx.Lock()
	defer t.mtx.Unlock()

	var names []string

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

// RecordPodEvent records pod event via PodEventRecorder
func (t *Tracer) RecordPodEvent(podName, podNamespace, eventType, reason, message string) error {
	if t.podEvent == nil {
		return errors.New("no pod event recorder registered")
	}

	return t.podEvent(podName, podNamespace, eventType, reason, message)
}

// RecordNodeEvent records node event via PodEventRecorder
func (t *Tracer) RecordNodeEvent(eventType, reason, message string) error {
	if t.nodeEvent == nil {
		return errors.New("no node event recorder registered")
	}

	t.nodeEvent(eventType, reason, message)
	return nil
}

// GetResourceMapping gives the resource mapping from the handler
// if the handler has not been registered, there will be error
func (t *Tracer) GetResourceMapping() ([]*PodMapping, error) {
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
func RegisterResourceMapping(handler ResMapping) {
	defaultTracer.RegisterResourceMapping(handler)
}

// Unregister removes TraceHandler from tracer. do nothing if not found
func Unregister(typ, resourceName string) {
	defaultTracer.Unregister(typ, resourceName)
}

// RegisterEventRecorder registers pod & node event recorder to a tracer
func RegisterEventRecorder(node NodeEventRecorder, pod PodEventRecorder) {
	defaultTracer.RegisterEventRecorder(node, pod)
}

// RecordPodEvent records pod event via PodEventRecorder
func RecordPodEvent(podName, podNamespace, eventType, reason, message string) error {
	return defaultTracer.RecordPodEvent(podName, podNamespace, eventType, reason, message)
}

// RecordNodeEvent records node event via PodEventRecorder
func RecordNodeEvent(eventType, reason, message string) error {
	return defaultTracer.RecordNodeEvent(eventType, reason, message)
}

// NewTracer creates a new tracer
func NewTracer() *Tracer {
	return &Tracer{
		mtx:      sync.Mutex{},
		traceMap: make(map[string]resourceMap),
	}
}
