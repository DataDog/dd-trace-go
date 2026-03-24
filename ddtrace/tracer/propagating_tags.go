// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strconv"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/locking/assert"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

func (t *trace) hasPropagatingTag(k string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.propagatingTags[k]
	return ok
}

func (t *trace) propagatingTag(k string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.propagatingTags[k]
}

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
	if source := t.propagatingTags[key]; source != "" {
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
	if t.propagatingTags == nil {
		t.propagatingTags = make(map[string]string, 1)
	}
	t.propagatingTags[key] = value
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
	delete(t.propagatingTags, key)
	if key == keyDecisionMaker {
		t.dm = 0
	}
}

// iteratePropagatingTags allows safe iteration through the propagating tags of a trace.
// the trace must not be modified during this call, as it is locked for reading.
//
// f should return whether the iteration should continue.
func (t *trace) iteratePropagatingTags(f func(k, v string) bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for k, v := range t.propagatingTags {
		if !f(k, v) {
			break
		}
	}
}

func (t *trace) replacePropagatingTags(tags map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.propagatingTags = tags
	if dm, ok := tags[keyDecisionMaker]; ok {
		t.dm = parseDecisionMaker(dm)
	} else {
		t.dm = 0
	}
}

func (t *trace) propagatingTagsLen() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.propagatingTags)
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
