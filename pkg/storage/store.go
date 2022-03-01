package storage

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/AliyunContainerService/terway/pkg/logger"

	"github.com/boltdb/bolt"
)

var log = logger.DefaultLogger.WithField("subSys", "storage")

// ErrNotFound key not found in store
var ErrNotFound = errors.New("not found")

// Storage persistent storage on disk
type Storage interface {
	Put(key string, value interface{}) error
	Get(key string) (interface{}, error)
	List() ([]interface{}, error)
	Delete(key string) error
}

// MemoryStorage is in memory storage
type MemoryStorage struct {
	lock  sync.RWMutex
	store map[string]interface{}
}

// NewMemoryStorage return new in memory storage
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		store: make(map[string]interface{}),
	}
}

// Put somethings into memory storage
func (m *MemoryStorage) Put(key string, value interface{}) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.store[key] = value
	return nil
}

// Get value in memory storage
func (m *MemoryStorage) Get(key string) (interface{}, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	value, ok := m.store[key]
	if !ok {
		return nil, ErrNotFound
	}
	return value, nil
}

// List values in memory storage
func (m *MemoryStorage) List() ([]interface{}, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	var ret []interface{}
	for _, v := range m.store {
		ret = append(ret, v)
	}
	return ret, nil
}

// Delete key in memory storage
func (m *MemoryStorage) Delete(key string) error {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.store, key)
	return nil
}

// Serializer interface to tell storage how to serialize object
type Serializer func(interface{}) ([]byte, error)

// Deserializer interface to tell storage how to deserialize
type Deserializer func([]byte) (interface{}, error)

// DiskStorage persistence storage on disk
type DiskStorage struct {
	db           *bolt.DB
	name         string
	memory       *MemoryStorage
	serializer   Serializer
	deserializer Deserializer
}

// NewDiskStorage return new disk storage
func NewDiskStorage(name string, path string, serializer Serializer, deserializer Deserializer) (Storage, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}

	diskstorage := &DiskStorage{
		db:           db,
		name:         name,
		memory:       NewMemoryStorage(),
		serializer:   serializer,
		deserializer: deserializer,
	}

	err = diskstorage.load()

	if err != nil {
		return nil, err
	}

	return diskstorage, nil
}

// Put somethings into disk storage
func (d *DiskStorage) Put(key string, value interface{}) error {
	data, err := d.serializer(value)
	if err != nil {
		return err
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		return b.Put([]byte(key), data)
	})
	if err != nil {
		return err
	}
	return d.memory.Put(key, value)
}

// load all data from disk db
func (d *DiskStorage) load() error {
	err := d.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(d.name))
		return err
	})
	if err != nil {
		return err
	}

	err = d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		cursor := b.Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			log.Infof("load pod cache %s from db", k)
			obj, err := d.deserializer(v)
			if err != nil {
				return err
			}
			err = d.memory.Put(string(k), obj)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// Get value in disk storage
func (d *DiskStorage) Get(key string) (interface{}, error) {
	return d.memory.Get(key)
}

// List values in disk storage
func (d *DiskStorage) List() ([]interface{}, error) {
	return d.memory.List()
}

// Delete key in disk storage
func (d *DiskStorage) Delete(key string) error {
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(d.name))
		return b.Delete([]byte(key))
	})
	if err != nil {
		return err
	}
	return d.memory.Delete(key)
}
