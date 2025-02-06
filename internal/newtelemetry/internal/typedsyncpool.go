// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"sync"
)

// SyncPool is a wrapper around sync.Pool that provides type safety.
type SyncPool[T any] struct {
	pool *sync.Pool
}

// NewSyncPool creates a new SyncPool with the given new function.
func NewSyncPool[T any](new func() T) *SyncPool[T] {
	return &SyncPool[T]{
		pool: &sync.Pool{
			New: func() any {
				return new()
			},
		},
	}
}

func (sp *SyncPool[T]) Get() T {
	return sp.pool.Get().(T)
}

func (sp *SyncPool[T]) Put(v T) {
	sp.pool.Put(v)
}
