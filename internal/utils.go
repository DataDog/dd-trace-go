// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"sync"
	"sync/atomic"

	xsync "github.com/puzpuzpuz/xsync/v3"
)

// OtelTagsDelimeter is the separator between key-val pairs for OTEL env vars
const OtelTagsDelimeter = "="

// DDTagsDelimiter is the separator between key-val pairs for DD env vars
const DDTagsDelimiter = ":"

// LockMap uses an RWMutex to synchronize map access to allow for concurrent access.
// This should not be used for cases with heavy write load and performance concerns.
type LockMap struct {
	sync.RWMutex
	c uint32
	m map[string]string
}

func NewLockMap(m map[string]string) *LockMap {
	return &LockMap{m: m, c: uint32(len(m))}
}

// Iter iterates over all the map entries passing in keys and values to provided func f. Note this is READ ONLY.
func (l *LockMap) Iter(f func(key string, val string)) {
	c := atomic.LoadUint32(&l.c)
	if c == 0 { //Fast exit to avoid the cost of RLock/RUnlock for empty maps
		return
	}
	l.RLock()
	defer l.RUnlock()
	for k, v := range l.m {
		f(k, v)
	}
}

func (l *LockMap) Len() int {
	l.RLock()
	defer l.RUnlock()
	return len(l.m)
}

func (l *LockMap) Clear() {
	l.Lock()
	defer l.Unlock()
	l.m = map[string]string{}
	atomic.StoreUint32(&l.c, 0)
}

func (l *LockMap) Set(k, v string) {
	l.Lock()
	defer l.Unlock()
	if _, ok := l.m[k]; !ok {
		atomic.AddUint32(&l.c, 1)
	}
	l.m[k] = v
}

func (l *LockMap) Get(k string) string {
	l.RLock()
	defer l.RUnlock()
	return l.m[k]
}

// TODO(hannahkm): we only keep one of the following two implementations

// XSyncMapCounterMap uses xsync protect counter increments and reads during
// concurrent access.
type XSyncMapCounterMap struct {
	counts *xsync.MapOf[string, *xsync.Counter]
}

func NewXSyncMapCounterMap() *XSyncMapCounterMap {
	return &XSyncMapCounterMap{counts: xsync.NewMapOf[string, *xsync.Counter]()}
}

func (cm *XSyncMapCounterMap) Inc(key string) {
	val, _ := cm.counts.LoadOrCompute(key, func() *xsync.Counter {
		return xsync.NewCounter()
	})
	val.Inc()
}

func (cm *XSyncMapCounterMap) GetAndReset() map[string]int64 {
	ret := map[string]int64{}
	cm.counts.Range(func(key string, counter *xsync.Counter) bool {
		ret[key] = counter.Value()
		cm.counts.Delete(key)
		return true
	})
	return ret
}

// XSyncMapIntMap uses xsync and atomics to create a map for concurrent access.
type XSyncMapIntMap struct {
	counts *xsync.MapOf[string, *atomic.Int64]
}

func NewXSyncMapIntMap() *XSyncMapIntMap {
	return &XSyncMapIntMap{
		counts: xsync.NewMapOf[string, *atomic.Int64](),
	}
}

func (cm *XSyncMapIntMap) Inc(key string) {
	val, _ := cm.counts.LoadOrCompute(key, func() *atomic.Int64 {
		return &atomic.Int64{}
	})
	val.Add(1)
}

func (cm *XSyncMapIntMap) GetAndReset() map[string]int64 {
	ret := map[string]int64{}
	cm.counts.Range(func(key string, value *atomic.Int64) bool {
		ret[key] = int64(value.Load())
		cm.counts.Delete(key)
		return true
	})
	return ret
}
