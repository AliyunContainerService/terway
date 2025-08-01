package tracing

import (
	"errors"
	"testing"

	"github.com/AliyunContainerService/terway/rpc"
	"github.com/AliyunContainerService/terway/types/daemon"
)

// MockTraceHandler implements TraceHandler interface for testing
type MockTraceHandler struct {
	config  []MapKeyValueEntry
	trace   []MapKeyValueEntry
	execute func(cmd string, args []string, message chan<- string)
}

func (m *MockTraceHandler) Config() []MapKeyValueEntry {
	return m.config
}

func (m *MockTraceHandler) Trace() []MapKeyValueEntry {
	return m.trace
}

func (m *MockTraceHandler) Execute(cmd string, args []string, message chan<- string) {
	if m.execute != nil {
		m.execute(cmd, args, message)
	} else {
		message <- "mock execute result"
		close(message)
	}
}

// MockResMapping implements ResMapping interface for testing
type MockResMapping struct {
	mappings []*rpc.ResourceMapping
	err      error
}

func (m *MockResMapping) GetResourceMapping() ([]*rpc.ResourceMapping, error) {
	return m.mappings, m.err
}

// MockRes implements daemon.Res interface for testing
type MockRes struct {
	id     string
	typ    string
	status daemon.ResStatus
}

func (m *MockRes) GetID() string {
	return m.id
}

func (m *MockRes) GetType() string {
	return m.typ
}

func (m *MockRes) GetStatus() daemon.ResStatus {
	return m.status
}

func TestConstants(t *testing.T) {
	if ResourceTypeNetworkService == "" {
		t.Error("ResourceTypeNetworkService should not be empty")
	}
	if ResourceTypeResourcePool == "" {
		t.Error("ResourceTypeResourcePool should not be empty")
	}
	if ResourceTypeFactory == "" {
		t.Error("ResourceTypeFactory should not be empty")
	}
	if DisposeResourceFailed == "" {
		t.Error("DisposeResourceFailed should not be empty")
	}
	if AllocResourceFailed == "" {
		t.Error("AllocResourceFailed should not be empty")
	}
}

func TestMapKeyValueEntry(t *testing.T) {
	entry := MapKeyValueEntry{
		Key:   "test_key",
		Value: "test_value",
	}

	if entry.Key != "test_key" {
		t.Errorf("Expected key 'test_key', got '%s'", entry.Key)
	}
	if entry.Value != "test_value" {
		t.Errorf("Expected value 'test_value', got '%s'", entry.Value)
	}
}

func TestFakeResourcePoolStats(t *testing.T) {
	local := map[string]daemon.Res{
		"local1": &MockRes{id: "local1", typ: "test", status: daemon.ResStatusIdle},
	}
	remote := map[string]daemon.Res{
		"remote1": &MockRes{id: "remote1", typ: "test", status: daemon.ResStatusInUse},
	}

	stats := &FakeResourcePoolStats{
		Local:  local,
		Remote: remote,
	}

	localRes := stats.GetLocal()
	if len(localRes) != 1 {
		t.Errorf("Expected 1 local resource, got %d", len(localRes))
	}

	remoteRes := stats.GetRemote()
	if len(remoteRes) != 1 {
		t.Errorf("Expected 1 remote resource, got %d", len(remoteRes))
	}
}

func TestNewTracer(t *testing.T) {
	tracer := NewTracer()
	if tracer == nil {
		t.Error("NewTracer should not return nil")
	}
	if tracer.traceMap == nil {
		t.Error("traceMap should be initialized")
	}
}

func TestTracer_Register(t *testing.T) {
	tracer := NewTracer()
	handler := &MockTraceHandler{
		config: []MapKeyValueEntry{{Key: "test", Value: "value"}},
	}

	// Test successful registration
	err := tracer.Register("test_type", "test_resource", handler)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Test duplicate registration
	err = tracer.Register("test_type", "test_resource", handler)
	if err == nil {
		t.Error("Expected error for duplicate registration")
	}
}

func TestTracer_Unregister(t *testing.T) {
	tracer := NewTracer()
	handler := &MockTraceHandler{}

	// Register first
	tracer.Register("test_type", "test_resource", handler)

	// Test unregister existing
	tracer.Unregister("test_type", "test_resource")

	// Test unregister non-existing (should not panic)
	tracer.Unregister("test_type", "test_resource")
	tracer.Unregister("non_existing_type", "test_resource")
}

func TestTracer_GetTypes(t *testing.T) {
	tracer := NewTracer()
	handler := &MockTraceHandler{}

	// Register multiple types
	tracer.Register("type1", "resource1", handler)
	tracer.Register("type2", "resource1", handler)

	types := tracer.GetTypes()
	if len(types) != 2 {
		t.Errorf("Expected 2 types, got %d", len(types))
	}
}

func TestTracer_GetResourceNames(t *testing.T) {
	tracer := NewTracer()
	handler := &MockTraceHandler{}

	// Register multiple resources of same type
	tracer.Register("test_type", "resource1", handler)
	tracer.Register("test_type", "resource2", handler)

	names := tracer.GetResourceNames("test_type")
	if len(names) != 2 {
		t.Errorf("Expected 2 resource names, got %d", len(names))
	}

	// Test non-existing type
	names = tracer.GetResourceNames("non_existing_type")
	if len(names) != 0 {
		t.Errorf("Expected 0 resource names for non-existing type, got %d", len(names))
	}
}

func TestTracer_GetConfig(t *testing.T) {
	tracer := NewTracer()
	config := []MapKeyValueEntry{{Key: "test_key", Value: "test_value"}}
	handler := &MockTraceHandler{config: config}

	tracer.Register("test_type", "test_resource", handler)

	result, err := tracer.GetConfig("test_type", "test_resource")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 config entry, got %d", len(result))
	}

	// Test non-existing type
	_, err = tracer.GetConfig("non_existing_type", "test_resource")
	if err == nil {
		t.Error("Expected error for non-existing type")
	}
}

func TestTracer_GetTrace(t *testing.T) {
	tracer := NewTracer()
	trace := []MapKeyValueEntry{{Key: "trace_key", Value: "trace_value"}}
	handler := &MockTraceHandler{trace: trace}

	tracer.Register("test_type", "test_resource", handler)

	result, err := tracer.GetTrace("test_type", "test_resource")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 trace entry, got %d", len(result))
	}

	// Test non-existing type
	_, err = tracer.GetTrace("non_existing_type", "test_resource")
	if err == nil {
		t.Error("Expected error for non-existing type")
	}
}

func TestTracer_RegisterResourceMapping(t *testing.T) {
	tracer := NewTracer()
	mapping := &MockResMapping{}

	tracer.RegisterResourceMapping(mapping)

	if tracer.resourceMapping != mapping {
		t.Error("Resource mapping was not registered correctly")
	}
}

func TestTracer_RegisterEventRecorder(t *testing.T) {
	tracer := NewTracer()

	podRecorder := func(podName, podNamespace, eventType, reason, message string) error {
		return nil
	}
	nodeRecorder := func(eventType, reason, message string) {
		// do nothing
	}

	tracer.RegisterEventRecorder(nodeRecorder, podRecorder)

	if tracer.podEvent == nil {
		t.Error("Pod event recorder was not registered")
	}
	if tracer.nodeEvent == nil {
		t.Error("Node event recorder was not registered")
	}
}

func TestTracer_RecordPodEvent(t *testing.T) {
	tracer := NewTracer()
	recorded := false

	podRecorder := func(podName, podNamespace, eventType, reason, message string) error {
		recorded = true
		return nil
	}

	tracer.RegisterEventRecorder(nil, podRecorder)

	err := tracer.RecordPodEvent("test_pod", "test_namespace", "test_event", "test_reason", "test_message")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !recorded {
		t.Error("Pod event was not recorded")
	}

	// Test without recorder
	tracer2 := NewTracer()
	err = tracer2.RecordPodEvent("test_pod", "test_namespace", "test_event", "test_reason", "test_message")
	if err == nil {
		t.Error("Expected error when no pod event recorder is registered")
	}
}

func TestTracer_RecordNodeEvent(t *testing.T) {
	tracer := NewTracer()
	recorded := false

	nodeRecorder := func(eventType, reason, message string) {
		recorded = true
	}

	tracer.RegisterEventRecorder(nodeRecorder, nil)

	err := tracer.RecordNodeEvent("test_event", "test_reason", "test_message")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !recorded {
		t.Error("Node event was not recorded")
	}

	// Test without recorder
	tracer2 := NewTracer()
	err = tracer2.RecordNodeEvent("test_event", "test_reason", "test_message")
	if err == nil {
		t.Error("Expected error when no node event recorder is registered")
	}
}

func TestTracer_GetResourceMapping(t *testing.T) {
	tracer := NewTracer()
	mappings := []*rpc.ResourceMapping{
		{
			NetworkInterfaceID: "eni-test",
			MAC:                "00:11:22:33:44:55",
			Type:               "test",
			Status:             "active",
		},
	}

	mapping := &MockResMapping{mappings: mappings}
	tracer.RegisterResourceMapping(mapping)

	result, err := tracer.GetResourceMapping()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(result) != 1 {
		t.Errorf("Expected 1 mapping, got %d", len(result))
	}

	// Test without mapping handler
	tracer2 := NewTracer()
	_, err = tracer2.GetResourceMapping()
	if err == nil {
		t.Error("Expected error when no resource mapping handler is registered")
	}

	// Test with error
	tracer3 := NewTracer()
	mappingWithError := &MockResMapping{err: errors.New("test error")}
	tracer3.RegisterResourceMapping(mappingWithError)

	_, err = tracer3.GetResourceMapping()
	if err == nil {
		t.Error("Expected error from mapping handler")
	}
}

// Test package-level functions
func TestRegister(t *testing.T) {
	handler := &MockTraceHandler{}

	err := Register("test_type", "test_resource", handler)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Test duplicate registration
	err = Register("test_type", "test_resource", handler)
	if err == nil {
		t.Error("Expected error for duplicate registration")
	}
}

func TestUnregister(t *testing.T) {
	handler := &MockTraceHandler{}

	Register("test_type", "test_resource", handler)
	Unregister("test_type", "test_resource")

	// Should not panic
	Unregister("test_type", "test_resource")
}

func TestRegisterEventRecorder(t *testing.T) {
	podRecorder := func(podName, podNamespace, eventType, reason, message string) error {
		return nil
	}
	nodeRecorder := func(eventType, reason, message string) {
		// do nothing
	}

	RegisterEventRecorder(nodeRecorder, podRecorder)

	// Test that it was registered
	err := RecordPodEvent("test_pod", "test_namespace", "test_event", "test_reason", "test_message")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	err = RecordNodeEvent("test_event", "test_reason", "test_message")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestRecordPodEvent(t *testing.T) {
	// Test without recorder (should return error)
	defaultTracer.podEvent = nil
	err := RecordPodEvent("test_pod", "test_namespace", "test_event", "test_reason", "test_message")
	if err == nil {
		t.Error("Expected error when no pod event recorder is registered")
	}
}

func TestRecordNodeEvent(t *testing.T) {
	// Test without recorder (should return error)
	defaultTracer.nodeEvent = nil
	err := RecordNodeEvent("test_event", "test_reason", "test_message")
	if err == nil {
		t.Error("Expected error when no node event recorder is registered")
	}
}

// // Test default tracer initialization
func TestDefaultTracerInit(t *testing.T) {
	// The default tracer should be initialized in init()
	if defaultTracer.traceMap == nil {
		t.Error("Default tracer traceMap should be initialized")
	}
}
