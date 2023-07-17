package delegate

import (
	"context"
	"net"

	aliyunClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	register "github.com/AliyunContainerService/terway/pkg/controller"
	"github.com/AliyunContainerService/terway/pkg/controller/common"
	"github.com/AliyunContainerService/terway/pkg/controller/node"
	corev1 "k8s.io/api/core/v1"
	k8sErr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ register.Interface = &Delegate{}

// Delegate is the delegate for each node
// use nodeName to lookup node related client
type Delegate struct {
	client        client.Client
	defaultClient register.Interface
}

func NewDelegate(aliyun register.Interface, client client.Client) *Delegate {
	return &Delegate{defaultClient: aliyun, client: client}
}

func (d *Delegate) CreateNetworkInterface(ctx context.Context, trunk bool, vSwitchID string, securityGroups []string, resourceGroupID string, ipCount, ipv6Count int, eniTags map[string]string) (*aliyunClient.NetworkInterface, error) {
	nodeName := common.NodeNameFromCtx(ctx)
	l := log.FromContext(ctx)
	l.Info("CreateNetworkInterface", "nodeName", nodeName)
	if nodeName == "" {
		realClient, _, err := common.Became(ctx, d.defaultClient)
		if err != nil {
			return nil, err
		}
		return realClient.CreateNetworkInterface(ctx, trunk, vSwitchID, securityGroups, resourceGroupID, ipCount, ipv6Count, eniTags)
	}
	nodeClient, err := node.GetPoolManager(nodeName)
	if err != nil {
		return nil, err
	}
	c, err := nodeClient.GetClient()
	if err != nil {
		return nil, err
	}
	return c.CreateNetworkInterface(ctx, trunk, vSwitchID, securityGroups, resourceGroupID, ipCount, ipv6Count, eniTags)
}

func (d *Delegate) DescribeNetworkInterface(ctx context.Context, vpcID string, eniID []string, instanceID string, instanceType string, status string, tags map[string]string) ([]*aliyunClient.NetworkInterface, error) {
	realClient, _, err := common.Became(ctx, d.defaultClient)
	if err != nil {
		return nil, err
	}
	return realClient.DescribeNetworkInterface(ctx, vpcID, eniID, instanceID, instanceType, status, tags)
}

func (d *Delegate) AttachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	nodeName := common.NodeNameFromCtx(ctx)
	if nodeName == "" {
		return d.defaultClient.AttachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
	}
	nodeClient, err := node.GetPoolManager(nodeName)
	if err != nil {
		return err
	}
	c, err := nodeClient.GetClient()
	if err != nil {
		return err
	}
	return c.AttachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
}

func (d *Delegate) DetachNetworkInterface(ctx context.Context, eniID, instanceID, trunkENIID string) error {
	nodeName := common.NodeNameFromCtx(ctx)
	nodeClient, err := node.GetPoolManager(nodeName)
	if nodeName == "" || err != nil {
		return d.defaultClient.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
	}
	c, err := nodeClient.GetClient()
	if err != nil {
		return err
	}
	return c.DetachNetworkInterface(ctx, eniID, instanceID, trunkENIID)
}

func (d *Delegate) WaitForNetworkInterface(ctx context.Context, eniID string, status string, backoff wait.Backoff, ignoreNotExist bool) (*aliyunClient.NetworkInterface, error) {
	nodeName := common.NodeNameFromCtx(ctx)
	l := log.FromContext(ctx)
	l.Info("WaitForNetworkInterface", "nodeName", nodeName)
	nodeClient, err := node.GetPoolManager(nodeName)
	if nodeName == "" || (err != nil && ignoreNotExist) {
		// not use pool or node already gone
		realClient, _, err := common.Became(ctx, d.defaultClient)
		if err != nil {
			return nil, err
		}
		return realClient.WaitForNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
	}
	if err != nil {
		return nil, err
	}
	c, err := nodeClient.GetClient()
	if err != nil {
		return nil, err
	}
	return c.WaitForNetworkInterface(ctx, eniID, status, backoff, ignoreNotExist)
}

func (d *Delegate) DescribeVSwitchByID(ctx context.Context, vSwitchID string) (*vpc.VSwitch, error) {
	realClient, _, err := common.Became(ctx, d.defaultClient)
	if err != nil {
		return nil, err
	}
	return realClient.DescribeVSwitchByID(ctx, vSwitchID)
}

func (d *Delegate) ModifyNetworkInterfaceAttribute(ctx context.Context, eniID string, securityGroupIDs []string) error {
	realClient, _, err := common.Became(ctx, d.defaultClient)
	if err != nil {
		return err
	}

	return realClient.ModifyNetworkInterfaceAttribute(ctx, eniID, securityGroupIDs)
}

func (d *Delegate) DescribeInstanceTypes(ctx context.Context, types []string) ([]ecs.InstanceType, error) {
	return d.defaultClient.DescribeInstanceTypes(ctx, types)
}

func (d *Delegate) AssignPrivateIPAddress(ctx context.Context, eniID string, count int, idempotentKey string) ([]net.IP, error) {
	//TODO implement me
	panic("implement me")
}

func (d *Delegate) UnAssignPrivateIPAddresses(ctx context.Context, eniID string, ips []net.IP) error {
	//TODO implement me
	panic("implement me")
}

func (d *Delegate) AssignIpv6Addresses(ctx context.Context, eniID string, count int, idempotentKey string) ([]net.IP, error) {
	//TODO implement me
	panic("implement me")
}

func (d *Delegate) UnAssignIpv6Addresses(ctx context.Context, eniID string, ips []net.IP) error {
	//TODO implement me
	panic("implement me")
}

//nolint:unused
func (d *Delegate) nodeExist(nodeName string) (bool, error) {
	in := &corev1.Node{}
	err := d.client.Get(context.Background(), types.NamespacedName{Name: nodeName}, in)
	if err == nil {
		return false, nil
	}
	if k8sErr.IsNotFound(err) {
		return true, nil
	}
	return false, err
}
