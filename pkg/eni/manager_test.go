package eni

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var _ NetworkInterface = &timeOut{}

type timeOut struct {
}

func (o *timeOut) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	return make(chan *AllocResp), nil
}

func (o *timeOut) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool {
	return true
}

func (o *timeOut) Priority() int {
	return 0
}

func (o *timeOut) Dispose(n int) int {
	return 0
}

func (o *timeOut) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

var _ NetworkInterface = &success{}

type success struct {
	priority int
	IPv4     netip.Addr
}

func (s *success) Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace) {
	ch := make(chan *AllocResp)

	go func() {
		ch <- &AllocResp{
			NetworkConfigs: []NetworkResource{
				&LocalIPResource{
					PodID: "",
					ENI:   daemon.ENI{},
					IP: types.IPSet2{
						IPv4: s.IPv4,
					},
				}},
		}
	}()
	return ch, nil
}

func (s *success) Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool {
	return true
}

func (s *success) Priority() int {
	return s.priority
}

func (s *success) Dispose(n int) int {
	return 0
}

func (s *success) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

func TestManagerAllocateReturnsResourcesWhenSuccessful(t *testing.T) {
	mockNI := &success{}
	manager := NewManager(0, 0, 0, 0, []NetworkInterface{mockNI}, types.EniSelectionPolicyMostIPs, &FakeK8s{})

	resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{&LocalIPRequest{}},
	})

	assert.Nil(t, err)
	assert.NotNil(t, resources)
}

func TestManagerAllocateSelectionPolicy(t *testing.T) {
	ip, _ := netip.AddrFromSlice(net.ParseIP("192.168.0.1"))
	mockNI := &success{
		priority: 1,
		IPv4:     ip,
	}
	ip, _ = netip.AddrFromSlice(net.ParseIP("192.168.0.2"))
	mockNI2 := &success{
		priority: 2,
		IPv4:     ip,
	}

	{
		manager := NewManager(0, 0, 0, 0, []NetworkInterface{mockNI, mockNI2}, types.EniSelectionPolicyMostIPs, &FakeK8s{})

		resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
			ResourceRequests: []ResourceRequest{&LocalIPRequest{}},
		})

		assert.Nil(t, err)
		assert.NotNil(t, resources)
		assert.Equal(t, mockNI2.IPv4.String(), resources[0].ToStore()[0].IPv4)
	}

	{
		manager := NewManager(0, 0, 0, 0, []NetworkInterface{mockNI, mockNI2}, types.EniSelectionPolicyLeastIPs, &FakeK8s{})

		resources, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
			ResourceRequests: []ResourceRequest{&LocalIPRequest{}},
		})

		assert.Nil(t, err)
		assert.NotNil(t, resources)
		assert.Equal(t, mockNI.IPv4.String(), resources[0].ToStore()[0].IPv4)
	}
}

func TestManagerAllocateReturnsErrorWhenNoBackendCanHandleAllocation(t *testing.T) {
	manager := NewManager(0, 0, 0, 0, []NetworkInterface{}, types.EniSelectionPolicyMostIPs, &FakeK8s{})

	_, err := manager.Allocate(context.Background(), &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{&LocalIPRequest{}},
	})

	assert.NotNil(t, err)
}

func TestManagerAllocateWithTimeoutWhenAllocationFails(t *testing.T) {
	mockNI := &timeOut{}
	manager := NewManager(0, 0, 0, 0, []NetworkInterface{mockNI}, types.EniSelectionPolicyMostIPs, &FakeK8s{})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := manager.Allocate(ctx, &daemon.CNI{}, &AllocRequest{
		ResourceRequests: []ResourceRequest{&LocalIPRequest{}},
	})
	assert.NotNil(t, err)
}

type FakeK8s struct{}

func (f *FakeK8s) GetLocalPods() ([]*daemon.PodInfo, error) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetPod(ctx context.Context, namespace, name string, cache bool) (*daemon.PodInfo, error) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetServiceCIDR() *types.IPNetSet {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetNodeCidr() *types.IPNetSet {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) SetNodeAllocatablePod(count int) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) PatchNodeAnnotations(anno map[string]string) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) PatchPodIPInfo(info *daemon.PodInfo, ips string) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) PatchNodeIPResCondition(status corev1.ConditionStatus, reason, message string) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) RecordNodeEvent(eventType, reason, message string) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) RecordPodEvent(podName, podNamespace, eventType, reason, message string) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetNodeDynamicConfigLabel() string {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetDynamicConfigWithName(ctx context.Context, name string) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) SetCustomStatefulWorkloadKinds(kinds []string) error {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) WaitTrunkReady() (string, error) {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetTrunkID() string {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) GetClient() client.Client {
	//TODO implement me
	panic("implement me")
}

func (f *FakeK8s) PodExist(namespace, name string) (bool, error) {
	panic("implement me")
}
