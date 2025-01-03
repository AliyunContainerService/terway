package eni

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/atomic"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/AliyunContainerService/terway/pkg/k8s"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

var mgrLog = logf.Log.WithName("eni-manager")

var ipExhaustiveConditionPeriod = 10 * time.Minute

type NodeConditionHandler func(status corev1.ConditionStatus, reason, message string) error

type NodeCondition struct {
	factoryIPExhaustive      *atomic.Bool
	factoryIPExhaustiveTimer *time.Timer

	handler NodeConditionHandler
}

func (n *NodeCondition) Run() {
	for range n.factoryIPExhaustiveTimer.C {
		if n.factoryIPExhaustive.Load() {
			if n.handler != nil {
				if err := n.handler(corev1.ConditionTrue, types.IPResSufficientReason,
					fmt.Sprintf("node has sufficient IP or pass the exhaustive period: %v", ipExhaustiveConditionPeriod)); err != nil {
					mgrLog.Error(err, "set IPExhaustive condition failed")
				}
			}
			n.factoryIPExhaustive.Store(false)
		}
	}
}

func (n *NodeCondition) SetIPExhaustive() {
	if n.handler == nil {
		return
	}

	if !n.factoryIPExhaustive.Load() {
		n.factoryIPExhaustive.Store(true)
		if err := n.handler(corev1.ConditionFalse, types.IPResInsufficientReason,
			"node has insufficient IP"); err != nil {
			mgrLog.Error(err, "set IPExhaustive condition failed")
		}
		n.factoryIPExhaustiveTimer.Reset(ipExhaustiveConditionPeriod)
	}
}

func (n *NodeCondition) UnsetIPExhaustive() {
	if n.factoryIPExhaustive.Load() {
		n.factoryIPExhaustiveTimer.Reset(0)
	}
}

type Usage interface {
	Usage() (int, int, error)
}

type ReportStatus interface {
	Status() Status
}
type NetworkInterface interface {
	Allocate(ctx context.Context, cni *daemon.CNI, request ResourceRequest) (chan *AllocResp, []Trace)
	Release(ctx context.Context, cni *daemon.CNI, request NetworkResource) bool
	Priority() int
	Dispose(n int) int
	Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error
}

type ByPriority []NetworkInterface

func (n ByPriority) Len() int {
	return len(n)
}

func (n ByPriority) Less(i, j int) bool {
	return n[i].Priority() > n[j].Priority()
}

func (n ByPriority) Swap(i, j int) { n[i], n[j] = n[j], n[i] }

type Manager struct {
	sync.RWMutex
	networkInterfaces []NetworkInterface
	selectionPolicy   types.EniSelectionPolicy

	minIdles int
	maxIdles int
	total    int

	syncPeriod time.Duration

	node *NodeCondition
}

func (m *Manager) Run(ctx context.Context, wg *sync.WaitGroup, podResources []daemon.PodResources) error {
	// 1. load all eni
	for _, ni := range m.networkInterfaces {
		err := ni.Run(ctx, podResources, wg)
		if err != nil {
			return err
		}
	}

	go m.node.Run()

	// 2. start a goroutine to sync pool
	if m.syncPeriod > 0 {
		go wait.JitterUntil(func() {
			m.syncPool(ctx)
		}, m.syncPeriod, 1.0, true, ctx.Done())
	}
	return nil
}

// Allocate find the resource manager and send the request to it.
// Caller should roll back the allocated resource if any error happen.
func (m *Manager) Allocate(ctx context.Context, cni *daemon.CNI, req *AllocRequest) (NetworkResources, error) {
	result := make([]NetworkResource, 0, len(req.ResourceRequests))

	resultCh := make(chan NetworkResources)
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		// start a goroutine to collect the result
		for {
			select {
			case <-ctx.Done():
				close(done)
				return
			case resp, ok := <-resultCh:
				if !ok {
					close(done)
					return
				}
				result = append(result, resp...)
			}
		}
	}()

	wg := sync.WaitGroup{}

	var traces []Trace

	m.Lock()
	switch m.selectionPolicy {
	case types.EniSelectionPolicyLeastIPs:
		sort.Sort(sort.Reverse(ByPriority(m.networkInterfaces)))
	default:
		sort.Sort(ByPriority(m.networkInterfaces))
	}

	var err error
	for _, request := range req.ResourceRequests {

		var ch chan *AllocResp
		for _, ni := range m.networkInterfaces {
			var tr []Trace
			ch, tr = ni.Allocate(ctx, cni, request)
			if ch != nil {
				break
			}
			traces = append(traces, tr...)
		}

		if ch == nil {
			m.Unlock()
			// no eni can handle the allocation
			for _, t := range traces {
				if t.Condition == InsufficientVSwitchIP {
					m.node.SetIPExhaustive()
					break
				}
			}
			return nil, fmt.Errorf("no eni can handle the allocation")
		}

		wg.Add(1)

		go func() {
			defer wg.Done()

			select {
			case <-ctx.Done():
				break
			case resp, ok := <-ch:
				if !ok {
					err = fmt.Errorf("ctx done")
					cancel()
					break
				}
				if resp.Err != nil {
					err = resp.Err
					cancel()
					break
				}
				resultCh <- resp.NetworkConfigs
			}
		}()
	}
	m.Unlock()

	wg.Wait()

	// already send , close it
	close(resultCh)
	<-done

	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}

	return result, err
}

// Release find the resource manager and send the request to it.
func (m *Manager) Release(ctx context.Context, cni *daemon.CNI, req *ReleaseRequest) error {
	m.RLock()
	defer m.RUnlock()

	for _, networkResource := range req.NetworkResources {
		for _, ni := range m.networkInterfaces {
			ok := ni.Release(ctx, cni, networkResource)
			if ok {
				if networkResource.ResourceType() == ResourceTypeLocalIP {
					m.node.UnsetIPExhaustive()
				}
				break
			}
		}
	}

	// assume resource is released, as no backend can handle the resource.
	return nil
}

func (m *Manager) Status() []Status {
	m.RLock()
	defer m.RUnlock()

	var result []Status

	for _, v := range m.networkInterfaces {
		s, ok := v.(ReportStatus)
		if !ok {
			continue
		}
		result = append(result, s.Status())
	}

	return result
}

func (m *Manager) syncPool(ctx context.Context) {
	m.Lock()
	switch m.selectionPolicy {
	case types.EniSelectionPolicyLeastIPs:
		sort.Sort(ByPriority(m.networkInterfaces))
	default:
		sort.Sort(sort.Reverse(ByPriority(m.networkInterfaces)))
	}

	var idles, inuses int
	for _, ni := range m.networkInterfaces {
		usage, ok := ni.(Usage)
		if !ok {
			continue
		}
		idle, inuse, err := usage.Usage()
		if err != nil {
			mgrLog.Error(err, "sync pool error")
			continue
		}
		idles += idle
		inuses += inuse
	}

	toDel := idles - m.maxIdles
	if toDel > 0 {
		mgrLog.Info("sync pool", "toDel", toDel)
		for _, ni := range m.networkInterfaces {
			if toDel <= 0 {
				break
			}
			toDel -= ni.Dispose(toDel)
		}
	}

	m.Unlock()

	if idles+inuses >= m.total {
		return
	}

	toAdd := m.minIdles - idles

	if toAdd <= 0 {
		return
	}

	wg := sync.WaitGroup{}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	mgrLog.Info("sync pool", "toAdd", toAdd)

	for i := 0; i < toAdd; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			req := NewLocalIPRequest()
			req.NoCache = true

			_, err := m.Allocate(ctx, &daemon.CNI{}, &AllocRequest{
				ResourceRequests: []ResourceRequest{
					req,
				},
			})
			if err != nil {
				mgrLog.Error(err, "sync pool error")
			}
		}()
	}

	wg.Wait()
}

func NewManager(minIdles, maxIdles, total int, syncPeriod time.Duration, networkInterfaces []NetworkInterface, selectionPolicy types.EniSelectionPolicy, k8s k8s.Kubernetes) *Manager {
	if syncPeriod < 2*time.Minute && syncPeriod > 0 {
		syncPeriod = 2 * time.Minute
	}

	var handler NodeConditionHandler
	if k8s != nil {
		handler = k8s.PatchNodeIPResCondition
	}
	return &Manager{
		networkInterfaces: networkInterfaces,
		selectionPolicy:   selectionPolicy,
		minIdles:          minIdles,
		maxIdles:          maxIdles,
		total:             total,
		syncPeriod:        syncPeriod,
		node: &NodeCondition{
			factoryIPExhaustiveTimer: time.NewTimer(0),
			factoryIPExhaustive:      atomic.NewBool(true),
			handler:                  handler,
		},
	}
}
