package eni

import (
	"errors"
	"sync"
	"time"

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

func (b *BackoffManager) Get(key string, bo wait.Backoff) (time.Duration, error) {
	v, err, _ := b.single.Do(key, func() (interface{}, error) {

		vv, _ := b.store.LoadOrStore(key, &ResourceBackoff{
			Bo: bo,
		})
		actual := vv.(*ResourceBackoff)

		du := time.Until(actual.NextTS)
		if du > 0 {
			// don't do backoff, as the executing is too soon
			return du, nil
		}

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
