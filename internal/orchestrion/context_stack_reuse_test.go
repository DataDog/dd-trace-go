// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPushReuseLeaksBuriedRecycledEntry demonstrates the ABA leak in Push's
// reclaim drain. In the orchestrion.yml GLS weave, a tracer Span can be pushed
// on one goroutine, finished elsewhere, and then returned to the tracer's Span
// sync.Pool before this goroutine performs its next Push. Pool reuse calls
// GLSReset, which flips the mutable reclaim flag back to false, so the old
// stack entry now looks live even though it is a different logical span from an
// unrelated scope. Push therefore keeps the finished-then-recycled entry instead
// of draining it, causing one false-live pointer to leak per iteration.
func TestPushReuseLeaksBuriedRecycledEntry(t *testing.T) {
	s := contextStack(make(map[any][]any))
	k := stackTestKey{}

	const iterations = 1000
	for i := 0; i < iterations; i++ {
		span := &fakeReclaimable{id: i, reclaimed: false}
		s.Push(k, span)

		span.reclaimed = true  // finished cross-goroutine; the next Push should reclaim it
		span.reclaimed = false // Span pool reuse/GLSReset: now an unrelated logical span
	}

	// A correct drain must not retain the finished entries just because their
	// pooled object was reset before the next push. Current buggy code sees each
	// recycled entry as live and retains all of them, so depth grows with N.
	require.LessOrEqual(t, s.Depth(), 2, "finished-then-recycled entries leaked; depth = %d, want <= 2", s.Depth())
}

// TestPushReusePeekReturnsRecycledSpan demonstrates the correctness half of the
// same ABA hazard. The orchestrion.yml GLS weave can leave spanA on this
// goroutine's stack after it was finished elsewhere; if the tracer Span pool
// reuses that object before this goroutine pushes spanB, GLSReset flips the
// reclaim flag back to false. Push then treats spanA as live and buries it below
// spanB. After spanB is popped, Peek resurfaces spanA even though it represents
// a finished span object that was recycled for an unrelated scope, not this
// goroutine's active value.
func TestPushReusePeekReturnsRecycledSpan(t *testing.T) {
	s := contextStack(make(map[any][]any))
	k := stackTestKey{}

	spanA := &fakeReclaimable{id: 1, reclaimed: false}
	s.Push(k, spanA)
	spanA.reclaimed = true  // finished cross-goroutine; should be reclaimed on next Push
	spanA.reclaimed = false // Span pool reuse/GLSReset makes it falsely look live again

	spanB := &fakeReclaimable{id: 2, reclaimed: false}
	s.Push(k, spanB)
	require.Equal(t, spanB, s.Peek(k), "spanB should be active before pop")

	popped := s.Pop(k)
	require.Equal(t, spanB, popped, "pop should remove the live top span")

	assert.NotEqual(t, spanA, s.Peek(k), "finished-then-recycled spanA must not resurface as this goroutine's active value")
}
