package client

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"sync"

	"github.com/google/uuid"
	"k8s.io/utils/lru"
)

type IdempotentKeyGen interface {
	GenerateKey(paramHash string) string
	PutBack(paramHash string, uuid string)
}

// SimpleIdempotentKeyGenerator implements the generation and management of idempotency keys.
type SimpleIdempotentKeyGenerator struct {
	mu    sync.Mutex
	cache *lru.Cache
}

func NewIdempotentKeyGenerator() *SimpleIdempotentKeyGenerator {
	size, err := strconv.Atoi(os.Getenv("IDEMPOTENT_KEY_CACHE_SIZE"))
	if err != nil || size <= 0 {
		size = 500
	}

	return &SimpleIdempotentKeyGenerator{
		cache: lru.New(size),
	}
}

// GenerateKey generates an idempotency key based on the given parameter hash.
// multiple key is supported
func (g *SimpleIdempotentKeyGenerator) GenerateKey(paramHash string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	v, ok := g.cache.Get(paramHash)
	if ok {
		uuids := v.([]string)
		if len(uuids) > 0 {
			id := ""
			id, uuids = uuids[len(uuids)-1], uuids[:len(uuids)-1]

			if len(uuids) == 0 {
				g.cache.Remove(paramHash)
			} else {
				g.cache.Add(paramHash, uuids)
			}
			return id
		}
	}

	return uuid.NewString()
}

// PutBack adds the specified idempotency key back into the cache for reuse, associating it with the given parameter hash.
func (g *SimpleIdempotentKeyGenerator) PutBack(paramHash string, uuid string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	v, ok := g.cache.Get(paramHash)
	if ok {
		uuids := v.([]string)
		uuids = append(uuids, uuid)

		g.cache.Add(paramHash, uuids)
	} else {
		g.cache.Add(paramHash, []string{uuid})
	}
}

func md5Hash(obj any) string {
	out, _ := json.Marshal(obj)
	hash := md5.Sum(out)
	return hex.EncodeToString(hash[:])
}
