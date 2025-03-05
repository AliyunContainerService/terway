package status

import (
	"context"
	"sort"
	"sync"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"
)

type ctxMetaKey struct{}

func MetaCtx[T any](ctx context.Context) (*T, bool) {
	if metadata := ctx.Value(ctxMetaKey{}); metadata != nil {
		if m, ok := metadata.(*T); ok {
			return m, true
		}
	}
	// return nil to avoid mistake
	return nil, false
}

func WithMeta[T any](ctx context.Context, meta *T) context.Context {
	return context.WithValue(ctx, ctxMetaKey{}, meta)
}

type Cache[T any] struct {
	s sync.Map
}

// NewCache creates a new Cache instance.
func NewCache[T any]() *Cache[T] {
	return &Cache[T]{}
}

// Get retrieves the value associated with the given key.
func (c *Cache[T]) Get(key string) (*T, bool) {
	if value, ok := c.s.Load(key); ok {
		// Type assertion is safe because we control the type T.
		return value.(*T), true
	}
	return nil, false
}

// LoadOrStore stores the value associated with the given key.
func (c *Cache[T]) LoadOrStore(key string, value *T) (*T, bool) {
	actual, loaded := c.s.LoadOrStore(key, value)
	return actual.(*T), loaded
}

// Delete removes the value associated with the given key.
func (c *Cache[T]) Delete(key string) {
	c.s.Delete(key)
}

func NewNodeStatus(cardCount int) *NodeStatus {
	cards := make([]*Card, cardCount)

	for i := 0; i < cardCount; i++ {
		cards[i] = &Card{
			CardIndex:         i,
			NetworkInterfaces: sets.New[NetworkInterfaceID](),
		}
	}

	return &NodeStatus{
		NetworkCards: cards,
	}
}

type Card struct {
	CardIndex         int
	NetworkInterfaces sets.Set[NetworkInterfaceID]
}

type NetworkInterfaceID string

type NodeStatus struct {
	lock sync.Mutex

	NetworkCards []*Card
}

// RequestNetworkIndex prefer use negative for auto allocate
func (n *NodeStatus) RequestNetworkIndex(eniID string, preferIndex *int, numa *int) *int {
	n.lock.Lock()
	defer n.lock.Unlock()

	if preferIndex != nil && *preferIndex > len(n.NetworkCards) {
		return nil
	}

	// release the index if present
	n.detachNetworkIndexLocked(NetworkInterfaceID(eniID))

	selected := n.NetworkCards

	if preferIndex == nil {
		// auto allocate, find the least card
		sort.Slice(n.NetworkCards, func(i, j int) bool {
			// Compare the lengths of the maps at index i and j
			return len(n.NetworkCards[i].NetworkInterfaces) < len(n.NetworkCards[j].NetworkInterfaces)
		})

		if numa != nil {
			// filter the nic
			selected = lo.Filter(n.NetworkCards, func(item *Card, index int) bool {
				if item.CardIndex%2 == *numa {
					// keep
					return true
				}
				return false
			})
		}
	} else {
		selected = lo.Filter(n.NetworkCards, func(item *Card, index int) bool {
			if item.CardIndex == *preferIndex {
				// keep
				return true
			}
			return false
		})
	}
	if len(selected) == 0 {
		return nil
	}

	selected[0].NetworkInterfaces.Insert(NetworkInterfaceID(eniID))
	return &selected[0].CardIndex
}

func (n *NodeStatus) DetachNetworkIndex(eniID string) {
	n.lock.Lock()
	defer n.lock.Unlock()
	n.detachNetworkIndexLocked(NetworkInterfaceID(eniID))
}

func (n *NodeStatus) detachNetworkIndexLocked(eniID NetworkInterfaceID) {
	lo.ForEach(n.NetworkCards, func(item *Card, index int) {
		item.NetworkInterfaces.Delete(eniID)
	})
}
