// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"sync"
)

type TypedSyncMap[K comparable, V any] struct {
	sync.Map
}

func (m *TypedSyncMap[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		return
	}
	value = v.(V)
	return
}

func (m *TypedSyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.Map.Range(func(key, value interface{}) bool {
		return f(key.(K), value.(V))
	})
}

func (m *TypedSyncMap[K, V]) Len() int {
	count := 0
	m.Map.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

func (m *TypedSyncMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	v, loaded := m.Map.LoadOrStore(key, value)
	actual = v.(V)
	return
}

func (m *TypedSyncMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.Map.LoadAndDelete(key)
	if !loaded {
		return
	}
	value = v.(V)
	return
}
