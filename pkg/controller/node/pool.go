package node

import (
	"fmt"
	"sync"

	eni_pool "github.com/AliyunContainerService/terway/pkg/controller/pool"
)

var nodePool sync.Map

func GetPoolManager(nodeName string) (*Client, error) {
	v, ok := nodePool.Load(nodeName)
	if !ok {
		return nil, fmt.Errorf("node %s client not found", nodeName)
	}
	return v.(*Client), nil
}

type Client struct {
	client *eni_pool.Manager
	synced bool

	sync.Mutex
}

func (c *Client) GetSynced() bool {
	c.Lock()
	defer c.Unlock()
	return c.synced
}

func (c *Client) GetClient() *eni_pool.Manager {
	c.Lock()
	defer c.Unlock()
	return c.client
}

func (c *Client) SetClient(client *eni_pool.Manager) {
	c.Lock()
	c.client = client
	c.synced = true
	c.Unlock()
}
