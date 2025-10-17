package eni

import (
	"errors"
	"sync"
	"time"

	"github.com/AliyunContainerService/terway/pkg/backoff"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/util/wait"
)

var errTimeOut = errors.New("timeout")

type BackoffManager struct {
	store sync.Map

	single singleflight.Group
}

func NewBackoffManager() *BackoffManager {
	return &BackoffManager{}
}

func (b *BackoffManager) Get(key string, bo backoff.ExtendedBackoff) (time.Duration, error) {
	v, err, _ := b.single.Do(key, func() (interface{}, error) {

		vv, loaded := b.store.LoadOrStore(key, &ResourceBackoff{
			Bo: bo.Backoff,
		})
		actual := vv.(*ResourceBackoff)

		// If this is the first time (!loaded) and we have an initial delay, return it
		if !loaded && bo.InitialDelay > 0 {
			actual.NextTS = time.Now().Add(bo.InitialDelay)
			return bo.InitialDelay, nil
		}

		// Check if we need to wait before next retry
		du := time.Until(actual.NextTS)
		if du > 0 {
			// don't do backoff, as the executing is too soon
			return du, nil
		}

		// Execute the next backoff step
		if actual.Bo.Steps > 0 {
			next := actual.Bo.Step()
			actual.NextTS = time.Now().Add(next)
			return next, nil
		}
		return time.Duration(0), errTimeOut
	})

	return v.(time.Duration), err
}

// GetNextTS test only
func (b *BackoffManager) GetNextTS(key string) (time.Time, bool) {
	v, ok := b.store.Load(key)
	if !ok {
		return time.Time{}, false
	}
	actual := v.(*ResourceBackoff)
	return actual.NextTS, true
}

func (b *BackoffManager) Del(key string) {
	b.store.Delete(key)
}

type ResourceBackoff struct {
	NextTS time.Time
	Bo     wait.Backoff
}
