package main

import (
	"fmt"
	"hash/fnv"
	"sync"
)

type ShardedMap[K comparable, V any] interface {
	Get(key K) (V, bool)
	Set(key K, value V)
	Delete(key K)
	Keys() []K
}

type shardedMap[K comparable, V any] struct {
	m  []map[K]V
	mu []sync.RWMutex
}

// Contructor to config number of shards
func NewShardedMap[K comparable, V any](count int) ShardedMap[K, V] {
	m := make([]map[K]V, count)

	for i := range m {
		m[i] = make(map[K]V)
	}

	mu := make([]sync.RWMutex, count)

	return &shardedMap[K, V]{
		m:  m,
		mu: mu,
	}
}

// Return value and existence flag
func (sm *shardedMap[K, V]) Get(key K) (V, bool) {
	index := sm.HashKey(key)

	sm.mu[index].RLock()
	defer sm.mu[index].RUnlock()
	value, ok := sm.m[index][key]

	return value, ok
}

// Insert or Update
func (sm *shardedMap[K, V]) Set(key K, value V) {
	index := sm.HashKey(key)

	sm.mu[index].Lock()
	defer sm.mu[index].Unlock()
	sm.m[index][key] = value
}

// Remove key
func (sm *shardedMap[K, V]) Delete(key K) {
	index := sm.HashKey(key)

	sm.mu[index].Lock()
	defer sm.mu[index].Unlock()
	delete(sm.m[index], key)
}

// Return all keys (order doesn't matter)
func (sm *shardedMap[K, V]) Keys() []K {
	var res []K

	for index, mp := range sm.m {
		sm.mu[index].RLock()
		for key := range mp {
			res = append(res, key)
		}
		sm.mu[index].RUnlock()
	}

	return res
}

// Hashing for key distribution
func (sm *shardedMap[K, V]) HashKey(key K) uint64 {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%v", key)))
	index := h.Sum64()
	index = index % uint64(len(sm.m))

	return index
}
