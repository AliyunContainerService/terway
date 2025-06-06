// Code generated by mockery v2.53.4. DO NOT EDIT.

package mocks

import (
	context "context"

	client "sigs.k8s.io/controller-runtime/pkg/client"

	daemon "github.com/AliyunContainerService/terway/types/daemon"

	mock "github.com/stretchr/testify/mock"

	types "github.com/AliyunContainerService/terway/types"

	v1 "k8s.io/api/core/v1"
)

// Kubernetes is an autogenerated mock type for the Kubernetes type
type Kubernetes struct {
	mock.Mock
}

// GetClient provides a mock function with no fields
func (_m *Kubernetes) GetClient() client.Client {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetClient")
	}

	var r0 client.Client
	if rf, ok := ret.Get(0).(func() client.Client); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(client.Client)
		}
	}

	return r0
}

// GetDynamicConfigWithName provides a mock function with given fields: ctx, name
func (_m *Kubernetes) GetDynamicConfigWithName(ctx context.Context, name string) (string, error) {
	ret := _m.Called(ctx, name)

	if len(ret) == 0 {
		panic("no return value specified for GetDynamicConfigWithName")
	}

	var r0 string
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string) (string, error)); ok {
		return rf(ctx, name)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string) string); ok {
		r0 = rf(ctx, name)
	} else {
		r0 = ret.Get(0).(string)
	}

	if rf, ok := ret.Get(1).(func(context.Context, string) error); ok {
		r1 = rf(ctx, name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetLocalPods provides a mock function with no fields
func (_m *Kubernetes) GetLocalPods() ([]*daemon.PodInfo, error) {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetLocalPods")
	}

	var r0 []*daemon.PodInfo
	var r1 error
	if rf, ok := ret.Get(0).(func() ([]*daemon.PodInfo, error)); ok {
		return rf()
	}
	if rf, ok := ret.Get(0).(func() []*daemon.PodInfo); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]*daemon.PodInfo)
		}
	}

	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetNodeDynamicConfigLabel provides a mock function with no fields
func (_m *Kubernetes) GetNodeDynamicConfigLabel() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetNodeDynamicConfigLabel")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// GetPod provides a mock function with given fields: ctx, namespace, name, cache
func (_m *Kubernetes) GetPod(ctx context.Context, namespace string, name string, cache bool) (*daemon.PodInfo, error) {
	ret := _m.Called(ctx, namespace, name, cache)

	if len(ret) == 0 {
		panic("no return value specified for GetPod")
	}

	var r0 *daemon.PodInfo
	var r1 error
	if rf, ok := ret.Get(0).(func(context.Context, string, string, bool) (*daemon.PodInfo, error)); ok {
		return rf(ctx, namespace, name, cache)
	}
	if rf, ok := ret.Get(0).(func(context.Context, string, string, bool) *daemon.PodInfo); ok {
		r0 = rf(ctx, namespace, name, cache)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*daemon.PodInfo)
		}
	}

	if rf, ok := ret.Get(1).(func(context.Context, string, string, bool) error); ok {
		r1 = rf(ctx, namespace, name, cache)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetServiceCIDR provides a mock function with no fields
func (_m *Kubernetes) GetServiceCIDR() *types.IPNetSet {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetServiceCIDR")
	}

	var r0 *types.IPNetSet
	if rf, ok := ret.Get(0).(func() *types.IPNetSet); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*types.IPNetSet)
		}
	}

	return r0
}

// GetTrunkID provides a mock function with no fields
func (_m *Kubernetes) GetTrunkID() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for GetTrunkID")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// Node provides a mock function with no fields
func (_m *Kubernetes) Node() *v1.Node {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for Node")
	}

	var r0 *v1.Node
	if rf, ok := ret.Get(0).(func() *v1.Node); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*v1.Node)
		}
	}

	return r0
}

// NodeName provides a mock function with no fields
func (_m *Kubernetes) NodeName() string {
	ret := _m.Called()

	if len(ret) == 0 {
		panic("no return value specified for NodeName")
	}

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// PatchNodeAnnotations provides a mock function with given fields: anno
func (_m *Kubernetes) PatchNodeAnnotations(anno map[string]string) error {
	ret := _m.Called(anno)

	if len(ret) == 0 {
		panic("no return value specified for PatchNodeAnnotations")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(map[string]string) error); ok {
		r0 = rf(anno)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// PatchNodeIPResCondition provides a mock function with given fields: status, reason, message
func (_m *Kubernetes) PatchNodeIPResCondition(status v1.ConditionStatus, reason string, message string) error {
	ret := _m.Called(status, reason, message)

	if len(ret) == 0 {
		panic("no return value specified for PatchNodeIPResCondition")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(v1.ConditionStatus, string, string) error); ok {
		r0 = rf(status, reason, message)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// PatchPodIPInfo provides a mock function with given fields: info, ips
func (_m *Kubernetes) PatchPodIPInfo(info *daemon.PodInfo, ips string) error {
	ret := _m.Called(info, ips)

	if len(ret) == 0 {
		panic("no return value specified for PatchPodIPInfo")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(*daemon.PodInfo, string) error); ok {
		r0 = rf(info, ips)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// PodExist provides a mock function with given fields: namespace, name
func (_m *Kubernetes) PodExist(namespace string, name string) (bool, error) {
	ret := _m.Called(namespace, name)

	if len(ret) == 0 {
		panic("no return value specified for PodExist")
	}

	var r0 bool
	var r1 error
	if rf, ok := ret.Get(0).(func(string, string) (bool, error)); ok {
		return rf(namespace, name)
	}
	if rf, ok := ret.Get(0).(func(string, string) bool); ok {
		r0 = rf(namespace, name)
	} else {
		r0 = ret.Get(0).(bool)
	}

	if rf, ok := ret.Get(1).(func(string, string) error); ok {
		r1 = rf(namespace, name)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RecordNodeEvent provides a mock function with given fields: eventType, reason, message
func (_m *Kubernetes) RecordNodeEvent(eventType string, reason string, message string) {
	_m.Called(eventType, reason, message)
}

// RecordPodEvent provides a mock function with given fields: podName, podNamespace, eventType, reason, message
func (_m *Kubernetes) RecordPodEvent(podName string, podNamespace string, eventType string, reason string, message string) error {
	ret := _m.Called(podName, podNamespace, eventType, reason, message)

	if len(ret) == 0 {
		panic("no return value specified for RecordPodEvent")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(string, string, string, string, string) error); ok {
		r0 = rf(podName, podNamespace, eventType, reason, message)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SetCustomStatefulWorkloadKinds provides a mock function with given fields: kinds
func (_m *Kubernetes) SetCustomStatefulWorkloadKinds(kinds []string) error {
	ret := _m.Called(kinds)

	if len(ret) == 0 {
		panic("no return value specified for SetCustomStatefulWorkloadKinds")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func([]string) error); ok {
		r0 = rf(kinds)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// SetNodeAllocatablePod provides a mock function with given fields: count
func (_m *Kubernetes) SetNodeAllocatablePod(count int) error {
	ret := _m.Called(count)

	if len(ret) == 0 {
		panic("no return value specified for SetNodeAllocatablePod")
	}

	var r0 error
	if rf, ok := ret.Get(0).(func(int) error); ok {
		r0 = rf(count)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewKubernetes creates a new instance of Kubernetes. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewKubernetes(t interface {
	mock.TestingT
	Cleanup(func())
}) *Kubernetes {
	mock := &Kubernetes{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
