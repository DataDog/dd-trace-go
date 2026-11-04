// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTraceProtocolStateTransitions is the full transition table for the
// monotone lattice: protoUnknown < protoV1 < protoV04 (terminal). Every
// (from, evidence) pair is asserted against the single expected result, so
// the whole design is one table to check rather than a paragraph to trust.
func TestTraceProtocolStateTransitions(t *testing.T) {
	cases := []struct {
		from     traceProtocolState
		evidence traceProtocolState
		want     traceProtocolState
	}{
		{protoUnknown, protoUnknown, protoUnknown},
		{protoUnknown, protoV1, protoV1},
		{protoUnknown, protoV04, protoV04},
		{protoV1, protoUnknown, protoV1}, // a transient error is not evidence
		{protoV1, protoV1, protoV1},
		{protoV1, protoV04, protoV04},
		{protoV04, protoUnknown, protoV04}, // terminal
		{protoV04, protoV1, protoV04},      // terminal: cannot re-upgrade
		{protoV04, protoV04, protoV04},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("from=%d/evidence=%d", tc.from, tc.evidence), func(t *testing.T) {
			c := &config{}
			c.protocolState.Store(int32(tc.from))
			c.advanceTraceProtocolState(tc.evidence)
			assert.Equal(t, tc.want, traceProtocolState(c.protocolState.Load()))
		})
	}
}

// TestTraceProtocolStateAdvanceReportsWhetherItMoved verifies the return
// value advanceTraceProtocolState callers rely on to dedupe repeated,
// unchanged transitions (e.g. downgradeAfterRejectedSend skipping its
// log/metric on a no-op call).
func TestTraceProtocolStateAdvanceReportsWhetherItMoved(t *testing.T) {
	c := &config{}
	assert.True(t, c.advanceTraceProtocolState(protoV1), "unknown -> v1 is a real move")
	assert.False(t, c.advanceTraceProtocolState(protoV1), "v1 -> v1 is not a move")
	assert.False(t, c.advanceTraceProtocolState(protoUnknown), "v1 -> unknown is not a move (would be a decrease)")
	assert.True(t, c.advanceTraceProtocolState(protoV04), "v1 -> v04 is a real move")
	assert.False(t, c.advanceTraceProtocolState(protoV1), "v04 -> v1 is not a move (terminal)")
}

// TestTraceProtocolStateConcurrentMonotonicity is the concurrency claim the
// design rests on: however goroutines interleave their evidence via
// advanceTraceProtocolState, the state only ever increases, and every
// goroutine eventually observes protoV04. Run with -race.
func TestTraceProtocolStateConcurrentMonotonicity(t *testing.T) {
	c := &config{}
	const goroutines = 32
	const rounds = 200

	var wg sync.WaitGroup
	var sawDecrease [goroutines]bool

	for g := range goroutines {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(seed))) //nolint:gosec // test-only, not security-sensitive
			var prevObserved traceProtocolState
			for range rounds {
				evidence := traceProtocolState(r.Intn(3))
				c.advanceTraceProtocolState(evidence)
				cur := traceProtocolState(c.protocolState.Load())
				if cur < prevObserved {
					sawDecrease[seed] = true
				}
				prevObserved = cur
			}
		}(g)
	}
	wg.Wait()

	for g := range goroutines {
		assert.False(t, sawDecrease[g], "goroutine %d observed the state decrease", g)
	}
	// With 32 goroutines each trying protoV04 with ~1/3 probability across 200
	// rounds, the final state is protoV04 with overwhelming probability.
	assert.Equal(t, protoV04, traceProtocolState(c.protocolState.Load()))
}
