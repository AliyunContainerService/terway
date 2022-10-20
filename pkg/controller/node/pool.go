package node

import (
	"errors"
	"fmt"
	"sync"

	eni_pool "github.com/AliyunContainerService/terway/pkg/controller/pool"
)

var ErrNodeNotSynced = errors.New("node is not synced")

var nodePool sync.Map

func GetPoolManager(nodeName string) (*Client, error) {
	v, ok := nodePool.Load(nodeName)
	if !ok {
		return nil, fmt.Errorf("node %s client not found", nodeName)
	}
	return v.(*Client), nil
}

// Client represent the client for each node
// several cases need to be concerned
// 1. synchronous sync k8s nodes
// 2. async all nodes with enis
type Client struct {
	client *eni_pool.Manager

	sync.Mutex
}

func (c *Client) GetClient() (*eni_pool.Manager, error) {
	c.Lock()
	defer c.Unlock()
	if c.client == nil {
		return nil, ErrNodeNotSynced
	}
	return c.client, nil
}

func (c *Client) SetClient(client *eni_pool.Manager) {
	c.Lock()
	c.client = client
	c.Unlock()
}
