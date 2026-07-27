// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"maps"
	"strconv"
	_ "unsafe" // for go:linkname

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/locking/assert"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// loadPropagatingTags returns the current propagating-tags snapshot. The
// returned map must not be modified.
func (t *trace) loadPropagatingTags() map[string]string {
	if snap := t.propagatingTags.Load(); snap != nil {
		return snap.(map[string]string)
	}
	return nil
}

// copyOnWriteSet stores key=value in the propagating-tags map using
// copy-on-write so concurrent readers remain lock-free. Caller must hold t.mu.
// +checklocks:t.mu
func (t *trace) copyOnWriteSet(key, value string) {
	assert.RWMutexLocked(&t.mu)
	oldMap := t.loadPropagatingTags()
	if oldMap != nil && oldMap[key] == value {
		return // value unchanged — skip allocation
	}
	var m map[string]string
	if oldMap == nil {
		m = map[string]string{key: value}
	} else {
		n := len(oldMap)
		if _, exists := oldMap[key]; !exists {
			n++ // new key, not an update
		}
		m = make(map[string]string, n)
		maps.Copy(m, oldMap)
		m[key] = value
	}
	t.propagatingTags.Store(m)
}

// copyOnWriteDelete removes key from the propagating-tags map.
// Caller must hold t.mu.
// +checklocks:t.mu
func (t *trace) copyOnWriteDelete(key string) {
	assert.RWMutexLocked(&t.mu)
	oldMap := t.loadPropagatingTags()
	if oldMap == nil {
		return
	}
	if _, exists := oldMap[key]; !exists {
		return
	}
	m := make(map[string]string, len(oldMap)-1)
	for k, v := range oldMap {
		if k != key {
			m[k] = v
		}
	}
	t.propagatingTags.Store(m)
}

func (t *trace) hasPropagatingTag(k string) bool {
	m := t.loadPropagatingTags()
	if m == nil {
		return false
	}
	_, ok := m[k]
	return ok
}

func (t *trace) propagatingTag(k string) string {
	if m := t.loadPropagatingTags(); m != nil {
		return m[k]
	}
	return ""
}

// iteratePropagatingTags iterates the propagating tags without holding any
// lock. f should return false to stop iteration.
func (t *trace) iteratePropagatingTags(f func(k, v string) bool) {
	for k, v := range t.loadPropagatingTags() {
		if !f(k, v) {
			break
		}
	}
}

// setPropagatingTagUnsafe writes key=value directly into the propagating-tags
// map without copy-on-write. Caller must guarantee the trace is not yet visible
// to other goroutines (e.g. during header extraction, before SpanContext is
// returned). The currently stored map is mutated in place; a new map is
// allocated (and stored atomically) only when the atomic.Value holds nil. If
// an intervening CoW write (e.g. from setSamplingPriority) has swapped in a
// new map, this call loads and mutates that newer map instead.
// +checklocksignore - Caller guarantees no concurrent access.
func (t *trace) setPropagatingTagUnsafe(key, value string) {
	m := t.loadPropagatingTags()
	if m == nil {
		m = make(map[string]string, 4)
		t.propagatingTags.Store(m)
	}
	m[key] = value
	if key == keyDecisionMaker {
		t.dm = parseDecisionMaker(value)
	}
}

// setPropagatingTag sets the key/value pair as a trace propagating tag.
func (t *trace) setPropagatingTag(key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setPropagatingTagLocked(key, value)
}

// setSpanContextPropagatingTag is a testing helper reachable via go:linkname
// from instrumentation/testutils. It exists to avoid unsafe struct mirroring
// in tests. Panics if ctx has no trace attached (i.e. was not created from a span).
//
//go:linkname setSpanContextPropagatingTag
func setSpanContextPropagatingTag(ctx *SpanContext, k, v string) {
	ctx.trace.setPropagatingTag(k, v)
}

func (t *trace) setTraceSourcePropagatingTag(key string, value internal.TraceSource) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// If there is already a TraceSource value set in the trace
	// we need to add the new value to the bitmask.
	if source := t.propagatingTag(key); source != "" {
		tSource, err := internal.ParseTraceSource(source)
		if err != nil {
			log.Error("failed to parse trace source tag: %s", err.Error())
		}
		tSource |= value
		t.setPropagatingTagLocked(key, tSource.String())
		return
	}
	t.setPropagatingTagLocked(key, value.String())
}

// setPropagatingTagLocked sets the key/value pair as a trace propagating tag.
// Not safe for concurrent use, setPropagatingTag should be used instead in that case.
// +checklocks:t.mu
func (t *trace) setPropagatingTagLocked(key, value string) {
	assert.RWMutexLocked(&t.mu)
	t.copyOnWriteSet(key, value)
	if key == keyDecisionMaker {
		t.dm = parseDecisionMaker(value)
	}
}

// unsetPropagatingTag deletes the key/value pair from the trace's propagated tags.
func (t *trace) unsetPropagatingTag(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unsetPropagatingTagLocked(key)
}

// +checklocks:t.mu
func (t *trace) unsetPropagatingTagLocked(key string) {
	assert.RWMutexLocked(&t.mu)
	t.copyOnWriteDelete(key)
	if key == keyDecisionMaker {
		t.dm = 0
	}
}

func (t *trace) replacePropagatingTags(tags map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(tags) == 0 {
		// Typed nil keeps the atomic.Value type consistent across all stores
		// (Store(nil) would panic; a typed nil is a valid same-type value).
		t.propagatingTags.Store(map[string]string(nil))
		t.dm = 0
	} else {
		// Clone so that the stored snapshot is immutable and independent of
		// whatever the caller holds.
		cp := maps.Clone(tags)
		t.propagatingTags.Store(cp)
		if dm, ok := cp[keyDecisionMaker]; ok {
			t.dm = parseDecisionMaker(dm)
		} else {
			t.dm = 0
		}
	}
}

func (t *trace) propagatingTagsLen() int {
	return len(t.loadPropagatingTags())
}

// parseDecisionMaker parses the decision maker string (e.g. "-4") into
// its absolute uint32 form for v1 protocol encoding.
func parseDecisionMaker(dm string) uint32 {
	v, err := strconv.ParseInt(dm, 10, 32)
	if err != nil {
		log.Error("failed to convert decision maker to uint32: %s", err.Error())
		return 0
	}
	if v < 0 {
		v = -v
	}
	return uint32(v)
}

func (t *trace) decisionMaker() uint32 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dm
}
