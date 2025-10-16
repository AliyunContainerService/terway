package node

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	networkv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/spf13/viper"
)

// NodeType represents the type of node (ECS or EFLO)
type NodeType string

const (
	NodeTypeECS  NodeType = "ECS"
	NodeTypeEFLO NodeType = "EFLO"
)

// NodeFactory builds test Node objects using the builder pattern.
// It provides a fluent API for creating nodes with various configurations
// for ECS and EFLO types with different capabilities.
//
// Example usage:
//
//	node := NewNodeFactory("test-node").
//	    WithECS().
//	    WithNodeCap(3, 10, 10).
//	    WithIPv4(true).
//	    WithIPv6(true).
//	    Build()
type NodeFactory struct {
	name           string
	nodeType       NodeType
	instanceID     string
	instanceType   string
	regionID       string
	zone           string
	adapters       int
	totalAdapters  int
	ipv4PerAdapter int
	ipv6PerAdapter int
	enableIPv4     bool
	enableIPv6     bool
	enableTrunk    bool
	enableERDMA    bool
	vswitchOptions []string
	securityGroups []string
	minPoolSize    int
	maxPoolSize    int
	flavor         []networkv1beta1.Flavor
	labels         map[string]string
	existingENIs   map[string]*networkv1beta1.Nic
	tags           map[string]string
	tagFilter      map[string]string
}

// NewNodeFactory creates a new NodeFactory with sensible defaults.
// By default, creates an ECS node with basic configuration.
func NewNodeFactory(name string) *NodeFactory {
	return &NodeFactory{
		name:           name,
		nodeType:       NodeTypeECS,
		instanceID:     "i-test-instance",
		instanceType:   "ecs.g6.large",
		regionID:       "cn-hangzhou",
		zone:           "cn-hangzhou-k",
		adapters:       3,
		totalAdapters:  2,
		ipv4PerAdapter: 10,
		ipv6PerAdapter: 10,
		enableIPv4:     true,
		enableIPv6:     false,
		enableTrunk:    false,
		enableERDMA:    false,
		vswitchOptions: []string{"vsw-test"},
		securityGroups: []string{"sg-test"},
		minPoolSize:    0,
		maxPoolSize:    0,
		labels:         make(map[string]string),
		existingENIs:   make(map[string]*networkv1beta1.Nic),
		tags:           make(map[string]string),
		tagFilter:      nil,
	}
}

// WithECS configures the node as an ECS type node.
func (f *NodeFactory) WithECS() *NodeFactory {
	f.nodeType = NodeTypeECS
	// Remove EFLO label if present
	delete(f.labels, "alibabacloud.com/lingjun-worker")
	return f
}

// WithEFLO configures the node as an EFLO type node by adding the lingjun label.
func (f *NodeFactory) WithEFLO() *NodeFactory {
	f.nodeType = NodeTypeEFLO
	f.labels["alibabacloud.com/lingjun-worker"] = "true"
	return f
}

// WithInstanceID sets a custom instance ID.
func (f *NodeFactory) WithInstanceID(id string) *NodeFactory {
	f.instanceID = id
	return f
}

// WithInstanceType sets a custom instance type.
func (f *NodeFactory) WithInstanceType(instanceType string) *NodeFactory {
	f.instanceType = instanceType
	return f
}

// WithZone sets the zone ID.
func (f *NodeFactory) WithZone(zone string) *NodeFactory {
	f.zone = zone
	return f
}

// WithNodeCap configures the node capacity for adapters and IPs per adapter.
func (f *NodeFactory) WithNodeCap(adapters, ipv4PerAdapter, ipv6PerAdapter int) *NodeFactory {
	f.adapters = adapters
	f.ipv4PerAdapter = ipv4PerAdapter
	f.ipv6PerAdapter = ipv6PerAdapter
	if adapters > 0 {
		f.totalAdapters = adapters - 1 // total secondary adapters
	}
	return f
}

// WithIPv4 enables or disables IPv4.
func (f *NodeFactory) WithIPv4(enabled bool) *NodeFactory {
	f.enableIPv4 = enabled
	return f
}

// WithIPv6 enables or disables IPv6.
func (f *NodeFactory) WithIPv6(enabled bool) *NodeFactory {
	f.enableIPv6 = enabled
	return f
}

// WithTrunk enables or disables trunk ENI support.
func (f *NodeFactory) WithTrunk(enabled bool) *NodeFactory {
	f.enableTrunk = enabled
	return f
}

// WithERDMA enables or disables ERDMA support.
func (f *NodeFactory) WithERDMA(enabled bool) *NodeFactory {
	f.enableERDMA = enabled
	return f
}

// WithVSwitches sets the vSwitch options.
func (f *NodeFactory) WithVSwitches(vswitches ...string) *NodeFactory {
	f.vswitchOptions = vswitches
	return f
}

// WithSecurityGroups sets the security group IDs.
func (f *NodeFactory) WithSecurityGroups(sgs ...string) *NodeFactory {
	f.securityGroups = sgs
	return f
}

// WithPool configures the IP pool size.
func (f *NodeFactory) WithPool(minSize, maxSize int) *NodeFactory {
	f.minPoolSize = minSize
	f.maxPoolSize = maxSize
	return f
}

// WithFlavor sets the flavor configuration for ENI types.
func (f *NodeFactory) WithFlavor(flavor []networkv1beta1.Flavor) *NodeFactory {
	f.flavor = flavor
	return f
}

// WithDefaultSecondaryFlavor adds a default secondary ENI flavor.
func (f *NodeFactory) WithDefaultSecondaryFlavor(count int) *NodeFactory {
	f.flavor = []networkv1beta1.Flavor{
		{
			NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
			NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
			Count:                       count,
		},
	}
	return f
}

// WithLabels sets custom labels on the node.
func (f *NodeFactory) WithLabels(labels map[string]string) *NodeFactory {
	for k, v := range labels {
		f.labels[k] = v
	}
	return f
}

// WithTags sets custom tags for ENI creation.
func (f *NodeFactory) WithTags(tags map[string]string) *NodeFactory {
	f.tags = tags
	return f
}

// WithTagFilter sets tag filter for ENI selection.
func (f *NodeFactory) WithTagFilter(tagFilter map[string]string) *NodeFactory {
	f.tagFilter = tagFilter
	return f
}

// WithExistingENI adds an existing ENI to the node status.
func (f *NodeFactory) WithExistingENI(eni *networkv1beta1.Nic) *NodeFactory {
	f.existingENIs[eni.ID] = eni
	return f
}

// WithExistingENIs adds multiple existing ENIs to the node status.
func (f *NodeFactory) WithExistingENIs(enis ...*networkv1beta1.Nic) *NodeFactory {
	for _, eni := range enis {
		f.existingENIs[eni.ID] = eni
	}
	return f
}

// Build constructs and returns the Node object.
func (f *NodeFactory) Build() *networkv1beta1.Node {
	node := &networkv1beta1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   f.name,
			Labels: f.labels,
		},
		Spec: networkv1beta1.NodeSpec{
			NodeMetadata: networkv1beta1.NodeMetadata{
				InstanceID:   f.instanceID,
				InstanceType: f.instanceType,
				RegionID:     f.regionID,
				ZoneID:       f.zone,
			},
			NodeCap: networkv1beta1.NodeCap{
				Adapters:       f.adapters,
				TotalAdapters:  f.totalAdapters,
				IPv4PerAdapter: f.ipv4PerAdapter,
				IPv6PerAdapter: f.ipv6PerAdapter,
			},
			ENISpec: &networkv1beta1.ENISpec{
				Tag:                 f.tags,
				TagFilter:           f.tagFilter,
				VSwitchOptions:      f.vswitchOptions,
				SecurityGroupIDs:    f.securityGroups,
				EnableIPv4:          f.enableIPv4,
				EnableIPv6:          f.enableIPv6,
				EnableERDMA:         f.enableERDMA,
				EnableTrunk:         f.enableTrunk,
				VSwitchSelectPolicy: "most",
			},
			Pool: &networkv1beta1.PoolSpec{
				MaxPoolSize: f.maxPoolSize,
				MinPoolSize: f.minPoolSize,
			},
			Flavor: f.flavor,
		},
		Status: networkv1beta1.NodeStatus{
			NetworkInterfaces: f.existingENIs,
		},
	}

	// Set default flavor if not specified
	if len(node.Spec.Flavor) == 0 {
		node.Spec.Flavor = []networkv1beta1.Flavor{
			{
				NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
				NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
				Count:                       f.totalAdapters,
			},
		}
	}

	return node
}

// ENIFactory builds test ENI objects.
// It can generate ENIs with specified configurations or auto-generate based on node quotas.
//
// Example usage:
//
//	enis := NewENIFactory().
//	    WithCount(2).
//	    WithType(networkv1beta1.ENITypeSecondary).
//	    WithIPv4Count(5).
//	    Build()
type ENIFactory struct {
	baseID      string
	count       int
	eniType     networkv1beta1.ENIType
	trafficMode networkv1beta1.NetworkInterfaceTrafficMode
	status      string
	vswitchID   string
	ipv4Base    string
	ipv4Count   int
	ipv6Base    string
	ipv6Count   int
	podIDs      []string
	macBase     string
}

// NewENIFactory creates a new ENIFactory with defaults.
func NewENIFactory() *ENIFactory {
	return &ENIFactory{
		baseID:      "eni",
		count:       1,
		eniType:     networkv1beta1.ENITypeSecondary,
		trafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
		status:      aliyunClient.ENIStatusInUse,
		vswitchID:   "vsw-test",
		ipv4Base:    "192.168.0",
		ipv4Count:   1,
		ipv6Base:    "fd00::",
		ipv6Count:   0,
		macBase:     "00:00:00:00:00",
	}
}

// WithBaseID sets the base ID for ENI generation (e.g., "eni" -> "eni-1", "eni-2").
func (f *ENIFactory) WithBaseID(baseID string) *ENIFactory {
	f.baseID = baseID
	return f
}

// WithCount sets the number of ENIs to generate.
func (f *ENIFactory) WithCount(n int) *ENIFactory {
	f.count = n
	return f
}

// WithType sets the ENI type.
func (f *ENIFactory) WithType(eniType networkv1beta1.ENIType) *ENIFactory {
	f.eniType = eniType
	return f
}

// WithTrafficMode sets the traffic mode.
func (f *ENIFactory) WithTrafficMode(mode networkv1beta1.NetworkInterfaceTrafficMode) *ENIFactory {
	f.trafficMode = mode
	return f
}

// WithStatus sets the ENI status.
func (f *ENIFactory) WithStatus(status string) *ENIFactory {
	f.status = status
	return f
}

// WithVSwitch sets the vSwitch ID.
func (f *ENIFactory) WithVSwitch(vswID string) *ENIFactory {
	f.vswitchID = vswID
	return f
}

// WithIPv4Count sets the number of IPv4 addresses to generate per ENI.
func (f *ENIFactory) WithIPv4Count(count int) *ENIFactory {
	f.ipv4Count = count
	return f
}

// WithIPv4Base sets the base IPv4 address for generation (e.g., "192.168.0").
func (f *ENIFactory) WithIPv4Base(base string) *ENIFactory {
	f.ipv4Base = base
	return f
}

// WithIPv6Count sets the number of IPv6 addresses to generate per ENI.
func (f *ENIFactory) WithIPv6Count(count int) *ENIFactory {
	f.ipv6Count = count
	return f
}

// WithIPv6Base sets the base IPv6 address for generation (e.g., "fd00::").
func (f *ENIFactory) WithIPv6Base(base string) *ENIFactory {
	f.ipv6Base = base
	return f
}

// WithAutoAssignPods configures automatic pod ID assignment to IPs.
func (f *ENIFactory) WithAutoAssignPods(podIDs []string) *ENIFactory {
	f.podIDs = podIDs
	return f
}

// BuildWithCustomIPs creates a single ENI with custom IP configurations.
// This is useful for testing specific IP statuses and configurations.
func BuildENIWithCustomIPs(eniID, status string, ipv4s, ipv6s map[string]*networkv1beta1.IP) *networkv1beta1.Nic {
	return &networkv1beta1.Nic{
		ID:                          eniID,
		Status:                      status,
		NetworkInterfaceType:        networkv1beta1.ENITypeSecondary,
		NetworkInterfaceTrafficMode: networkv1beta1.NetworkInterfaceTrafficModeStandard,
		IPv4:                        ipv4s,
		IPv6:                        ipv6s,
	}
}

// Build generates and returns the ENI objects.
func (f *ENIFactory) Build() []*networkv1beta1.Nic {
	enis := make([]*networkv1beta1.Nic, 0, f.count)

	for i := 0; i < f.count; i++ {
		eniID := fmt.Sprintf("%s-%d", f.baseID, i+1)
		macAddr := fmt.Sprintf("%s:%02d", f.macBase, i+1)

		eni := &networkv1beta1.Nic{
			ID:                          eniID,
			Status:                      f.status,
			MacAddress:                  macAddr,
			NetworkInterfaceType:        f.eniType,
			NetworkInterfaceTrafficMode: f.trafficMode,
			VSwitchID:                   f.vswitchID,
			IPv4:                        make(map[string]*networkv1beta1.IP),
			IPv6:                        make(map[string]*networkv1beta1.IP),
		}

		// Generate IPv4 addresses
		podIdx := 0
		for j := 0; j < f.ipv4Count; j++ {
			ipAddr := fmt.Sprintf("%s.%d", f.ipv4Base, i*100+j+1)
			ip := &networkv1beta1.IP{
				IP:      ipAddr,
				IPName:  fmt.Sprintf("ip-%s-%d", eniID, j+1),
				Status:  networkv1beta1.IPStatusValid,
				Primary: j == 0,
			}

			// Auto-assign pod if requested
			if podIdx < len(f.podIDs) {
				ip.PodID = f.podIDs[podIdx]
				ip.PodUID = fmt.Sprintf("uid-%s", f.podIDs[podIdx])
				podIdx++
			}

			eni.IPv4[ipAddr] = ip
		}

		// Generate IPv6 addresses
		for j := 0; j < f.ipv6Count; j++ {
			ipAddr := fmt.Sprintf("%s%x", f.ipv6Base, i*100+j+1)
			ip := &networkv1beta1.IP{
				IP:      ipAddr,
				IPName:  fmt.Sprintf("ipv6-%s-%d", eniID, j+1),
				Status:  networkv1beta1.IPStatusValid,
				Primary: j == 0,
			}

			// Auto-assign pod if requested and IPv4 was exhausted
			if podIdx < len(f.podIDs) {
				ip.PodID = f.podIDs[podIdx]
				ip.PodUID = fmt.Sprintf("uid-%s", f.podIDs[podIdx])
				podIdx++
			}

			eni.IPv6[ipAddr] = ip
		}

		enis = append(enis, eni)
	}

	return enis
}

// BuildForNode auto-generates ENIs based on node quotas and configuration.
// This is useful for creating a realistic ENI setup that matches the node's capabilities.
func (f *ENIFactory) BuildForNode(node *networkv1beta1.Node) []*networkv1beta1.Nic {
	// Use node's configuration to determine ENI count and IP counts
	f.count = node.Spec.NodeCap.TotalAdapters
	if node.Spec.ENISpec.EnableIPv4 {
		f.ipv4Count = node.Spec.NodeCap.IPv4PerAdapter
	} else {
		f.ipv4Count = 0
	}
	if node.Spec.ENISpec.EnableIPv6 {
		f.ipv6Count = node.Spec.NodeCap.IPv6PerAdapter
	} else {
		f.ipv6Count = 0
	}

	if len(node.Spec.ENISpec.VSwitchOptions) > 0 {
		f.vswitchID = node.Spec.ENISpec.VSwitchOptions[0]
	}

	return f.Build()
}

// ReconcilerBuilder constructs ReconcileNode objects for testing with proper dependencies.
//
// Example usage:
//
//	reconciler := NewReconcilerBuilder().
//	    WithClient(k8sClient).
//	    WithAliyun(mockAPI).
//	    WithDefaults().
//	    Build()
type ReconcilerBuilder struct {
	client       client.Client
	scheme       *runtime.Scheme
	aliyun       aliyunClient.OpenAPI
	vswpool      *vswpool.SwitchPool
	record       record.EventRecorder
	tracer       trace.Tracer
	eniBatchSize int
	gcPeriod     time.Duration
	syncPeriod   time.Duration
	v            *viper.Viper
}

// NewReconcilerBuilder creates a new ReconcilerBuilder with minimal defaults.
func NewReconcilerBuilder() *ReconcilerBuilder {
	return &ReconcilerBuilder{
		eniBatchSize: 5,
		gcPeriod:     5 * time.Minute,
		syncPeriod:   5 * time.Minute,
	}
}

// WithClient sets the Kubernetes client.
func (b *ReconcilerBuilder) WithClient(c client.Client) *ReconcilerBuilder {
	b.client = c
	return b
}

// WithScheme sets the runtime scheme.
func (b *ReconcilerBuilder) WithScheme(scheme *runtime.Scheme) *ReconcilerBuilder {
	b.scheme = scheme
	return b
}

// WithAliyun sets the Aliyun OpenAPI client.
func (b *ReconcilerBuilder) WithAliyun(api aliyunClient.OpenAPI) *ReconcilerBuilder {
	b.aliyun = api
	return b
}

// WithVSwitchPool sets the vSwitch pool.
func (b *ReconcilerBuilder) WithVSwitchPool(pool *vswpool.SwitchPool) *ReconcilerBuilder {
	b.vswpool = pool
	return b
}

// WithRecorder sets the event recorder.
func (b *ReconcilerBuilder) WithRecorder(r record.EventRecorder) *ReconcilerBuilder {
	b.record = r
	return b
}

// WithTracer sets the OpenTelemetry tracer.
func (b *ReconcilerBuilder) WithTracer(tracer trace.Tracer) *ReconcilerBuilder {
	b.tracer = tracer
	return b
}

// WithENIBatchSize sets the ENI batch size for operations.
func (b *ReconcilerBuilder) WithENIBatchSize(size int) *ReconcilerBuilder {
	b.eniBatchSize = size
	return b
}

// WithGCPeriod sets the garbage collection period.
func (b *ReconcilerBuilder) WithGCPeriod(period time.Duration) *ReconcilerBuilder {
	b.gcPeriod = period
	return b
}

// WithSyncPeriod sets the full sync period.
func (b *ReconcilerBuilder) WithSyncPeriod(period time.Duration) *ReconcilerBuilder {
	b.syncPeriod = period
	return b
}

// WithViper sets the viper configuration.
func (b *ReconcilerBuilder) WithViper(v *viper.Viper) *ReconcilerBuilder {
	b.v = v
	return b
}

// WithDefaults sets sensible defaults for testing (fake recorder, noop tracer, etc.).
func (b *ReconcilerBuilder) WithDefaults() *ReconcilerBuilder {
	if b.record == nil {
		b.record = record.NewFakeRecorder(1000)
	}
	if b.tracer == nil {
		b.tracer = noop.NewTracerProvider().Tracer("")
	}
	return b
}

// Build constructs and returns the ReconcileNode object.
func (b *ReconcilerBuilder) Build() *ReconcileNode {
	// Apply defaults if not set
	b.WithDefaults()

	return &ReconcileNode{
		client:             b.client,
		scheme:             b.scheme,
		aliyun:             b.aliyun,
		vswpool:            b.vswpool,
		record:             b.record,
		tracer:             b.tracer,
		eniBatchSize:       b.eniBatchSize,
		gcPeriod:           b.gcPeriod,
		fullSyncNodePeriod: b.syncPeriod,
		v:                  b.v,
	}
}
