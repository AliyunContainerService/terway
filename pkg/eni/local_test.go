package eni

import (
	"context"
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"

	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

func NewLocalTest(eni *daemon.ENI, factory factory.Factory, poolConfig *types.PoolConfig) *Local {
	l := &Local{
		eni:        eni,
		batchSize:  poolConfig.BatchSize,
		cap:        poolConfig.MaxIPPerENI,
		cond:       sync.NewCond(&sync.Mutex{}),
		ipv4:       make(Set),
		ipv6:       make(Set),
		enableIPv4: poolConfig.EnableIPv4,
		enableIPv6: poolConfig.EnableIPv6,
		factory:    factory,

		rateLimitEni: rate.NewLimiter(100, 100),
		rateLimitv4:  rate.NewLimiter(100, 100),
		rateLimitv6:  rate.NewLimiter(100, 100),
	}

	return l
}

func TestLocal_Release_ValidIPv4(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{})
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv4.Add(NewValidIP(netip.MustParseAddr("192.0.2.1"), false))
	local.ipv4[netip.MustParseAddr("192.0.2.1")].Allocate("pod-1")

	assert.True(t, local.Release(context.Background(), cni, request))
}

func TestLocal_Release_ValidIPv6(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{})
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv6: netip.MustParseAddr("fd00:46dd:e::1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	local.ipv6.Add(NewValidIP(netip.MustParseAddr("fd00:46dd:e::1"), false))
	local.ipv6[netip.MustParseAddr("fd00:46dd:e::1")].Allocate("pod-1")

	assert.True(t, local.Release(context.Background(), cni, request))
}

func TestLocal_Release_NilENI(t *testing.T) {
	local := NewLocalTest(nil, nil, &types.PoolConfig{})
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-1"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	assert.False(t, local.Release(context.Background(), cni, request))
}

func TestLocal_Release_DifferentENIID(t *testing.T) {
	local := NewLocalTest(&daemon.ENI{ID: "eni-1"}, nil, &types.PoolConfig{})
	request := &LocalIPResource{
		ENI: daemon.ENI{ID: "eni-2"},
		IP:  types.IPSet2{IPv4: netip.MustParseAddr("192.0.2.1")},
	}
	cni := &daemon.CNI{PodID: "pod-1"}

	assert.False(t, local.Release(context.Background(), cni, request))
}
