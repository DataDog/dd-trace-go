// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"sync"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
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

type state struct {
	counters    *xsync.MapOf[string, *xsync.Counter]
	inFlightOps *atomic.Int64
	cond        *sync.Cond
}

func newState() *state {
	return &state{
		counters:    xsync.NewMapOf[string, *xsync.Counter](),
		inFlightOps: &atomic.Int64{},
		cond:        sync.NewCond(&sync.Mutex{}),
	}
}

func (s *state) reset() {
	s.counters.Clear()
}

type CounterMap struct {
	// Pointer to the active state - allows atomic swapping.
	activeState atomic.Pointer[state]

	// Prevents concurrent GetAndReset operations.
	mu *sync.Mutex

	// Pool for state objects to reduce GC pressure
	statePool sync.Pool
}

func NewCounterMap() *CounterMap {
	cm := &CounterMap{
		mu: &sync.Mutex{},
		statePool: sync.Pool{
			New: func() any {
				return newState()
			},
		},
	}
	cm.activeState.Store(cm.getStateFromPool())
	return cm
}

// getStateFromPool retrieves a state from the pool or creates a new one
func (cm *CounterMap) getStateFromPool() *state {
	s := cm.statePool.Get().(*state)
	s.reset()
	return s
}

func (cm *CounterMap) Inc(key string) {
	// Get the current active state.
	// Before adding to the inFlightOps, lock to ensure no GetAndReset() can be run
	// Otherwise, we risk losing data due to concurrent Inc() and GetAndReset()
	cm.mu.Lock()
	state := cm.activeState.Load()

	state.cond.L.Lock()
	state.inFlightOps.Add(1)
	state.cond.L.Unlock()
	cm.mu.Unlock()

	defer func() {
		// Remove operation from in-flight list.
		// If this was the last in-flight operation, signal waiters
		state.cond.L.Lock()
		if state.inFlightOps.Add(-1) == 0 {
			state.cond.Signal()
		}
		state.cond.L.Unlock()
	}()

	// Try to load existing counter.
	counter, loaded := state.counters.Load(key)
	if !loaded {
		// Create a new counter.
		counter = xsync.NewCounter()
		actual, loaded := state.counters.LoadOrStore(key, counter)
		if loaded {
			// Another goroutine beat us to it, use their counter.
			counter = actual
		}
	}

	// Increment the counter.
	counter.Inc()
}

func (cm *CounterMap) GetAndReset() map[string]int64 {
	// Ensure only one GetAndReset operation runs at a time.
	// If another GetAndReset() is already running, we return early.
	if !cm.mu.TryLock() {
		return nil
	}
	defer cm.mu.Unlock()

	// Atomically swap the states.
	// Create a new empty state for new increments from the pool.
	newState := cm.getStateFromPool()
	oldState := cm.activeState.Swap(newState)

	// Wait for all in-flight operations on the old map to complete.
	oldState.cond.L.Lock()
	for oldState.inFlightOps.Load() > 0 {
		oldState.cond.Wait() // Releases the lock while waiting.
	}
	defer oldState.cond.L.Unlock()

	// Process the old state - no more in-flight operations on it.
	res := make(map[string]int64)
	oldState.counters.Range(func(key string, _ *xsync.Counter) bool {
		value, loaded := oldState.counters.Load(key)
		if loaded {
			res[key] = value.Value()
		}
		return true
	})

	// Return the old state to the pool.
	cm.statePool.Put(oldState)

	return res
}

// ToFloat64 attempts to convert value into a float64. If the value is an integer
// greater or equal to 2^53 or less than or equal to -2^53, it will not be converted
// into a float64 to avoid losing precision. If it succeeds in converting, toFloat64
// returns the value and true, otherwise 0 and false.
func ToFloat64(value any) (f float64, ok bool) {
	const maxFloat = (int64(1) << 53) - 1
	const minFloat = -maxFloat
	// If any other type is added here, remember to add it to the type switch in
	// the `span.SetTag` function to handle pointers to these supported types.
	switch i := value.(type) {
	case byte:
		return float64(i), true
	case float32:
		return float64(i), true
	case float64:
		return i, true
	case int:
		return float64(i), true
	case int8:
		return float64(i), true
	case int16:
		return float64(i), true
	case int32:
		return float64(i), true
	case int64:
		if i > maxFloat || i < minFloat {
			return 0, false
		}
		return float64(i), true
	case uint:
		return float64(i), true
	case uint16:
		return float64(i), true
	case uint32:
		return float64(i), true
	case uint64:
		if i > uint64(maxFloat) {
			return 0, false
		}
		return float64(i), true
	case samplernames.SamplerName:
		return float64(i), true
	default:
		return 0, false
	}
}
