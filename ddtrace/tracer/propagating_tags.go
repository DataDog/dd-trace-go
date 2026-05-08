// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"maps"
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/locking/assert"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// propagatingTags is stored as an atomic.Pointer to an immutable map so that
// readers (e.g. Inject / marshalPropagatingTags) are completely lock-free.
// Writers hold t.mu and use copy-on-write to produce a new immutable snapshot.

// copyOnWriteSet stores key=value in the propagating-tags map using
// copy-on-write so concurrent readers remain lock-free. Caller must hold t.mu.
// +checklocks:t.mu
func (t *trace) copyOnWriteSet(key, value string) {
	old := t.propagatingTags.Load()
	var m map[string]string
	if old == nil || *old == nil {
		m = map[string]string{key: value}
	} else {
		if (*old)[key] == value {
			return // value unchanged — skip allocation
		}
		m = make(map[string]string, len(*old)+1)
		maps.Copy(m, *old)
		m[key] = value
	}
	t.propagatingTags.Store(&m)
}

// copyOnWriteDelete removes key from the propagating-tags map.
// Caller must hold t.mu.
// +checklocks:t.mu
func (t *trace) copyOnWriteDelete(key string) {
	old := t.propagatingTags.Load()
	if old == nil {
		return
	}
	if _, exists := (*old)[key]; !exists {
		return
	}
	m := make(map[string]string, len(*old)-1)
	for k, v := range *old {
		if k != key {
			m[k] = v
		}
	}
	t.propagatingTags.Store(&m)
}

// --- Lock-free read accessors ---

func (t *trace) hasPropagatingTag(k string) bool {
	if m := t.propagatingTags.Load(); m != nil {
		_, ok := (*m)[k]
		return ok
	}
	return false
}

func (t *trace) propagatingTag(k string) string {
	if m := t.propagatingTags.Load(); m != nil {
		return (*m)[k]
	}
	return ""
}

// iteratePropagatingTags iterates the propagating tags without holding any
// lock. f should return false to stop iteration.
func (t *trace) iteratePropagatingTags(f func(k, v string) bool) {
	if m := t.propagatingTags.Load(); m != nil {
		for k, v := range *m {
			if !f(k, v) {
				break
			}
		}
	}
}

// --- Write accessors (acquire t.mu internally) ---

// setPropagatingTag sets the key/value pair as a trace propagating tag.
func (t *trace) setPropagatingTag(key, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setPropagatingTagLocked(key, value)
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
		t.propagatingTags.Store(nil)
		t.dm = 0
	} else {
		// Clone so that the stored snapshot is immutable and independent of
		// whatever the caller holds.
		cp := maps.Clone(tags)
		t.propagatingTags.Store(&cp)
		if dm, ok := cp[keyDecisionMaker]; ok {
			t.dm = parseDecisionMaker(dm)
		} else {
			t.dm = 0
		}
	}
}

func (t *trace) propagatingTagsLen() int {
	if m := t.propagatingTags.Load(); m != nil {
		return len(*m)
	}
	return 0
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
