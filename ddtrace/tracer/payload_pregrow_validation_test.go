// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

// Tests and benchmarks that validate the adaptive pre-grow strategy for
// payloadV04 (Approach B). The strategy: when agentTraceWriter creates a
// replacement payload after a size-triggered flush, it passes the outgoing
// payload's encoded size as a capacity hint. This lets the next fill cycle
// reuse a buffer that is already large enough, avoiding bytes.Buffer's
// doubling ramp-up (typically ~12 reallocations for a 4.75 MB payload).
//
// At 400K req/10s a fresh 4.75 MB payload is built and discarded ~10–67×/s
// (depending on per-trace size). Without the hint, each new cycle re-allocates
// ~2× the final payload size in intermediate buffers. With the hint from the
// previous cycle's actual encoded size the transient allocation drops by ~50%.

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkTraceKB returns a spanList whose encoded msgpack size is roughly kb kilobytes.
func mkTraceKB(kb int) spanList {
	s := newBasicSpan("pregrow.span")
	s.start = fixedTime
	s.meta.Set("data", strings.Repeat("x", kb*1024))
	return spanList{s}
}

// TestPayloadV04PreGrowWireIntegrity verifies that pre-growing the underlying
// buffer does not change the encoded wire bytes. Both a cold (no hint) and a
// pre-grown payload must produce byte-identical msgpack output for the same
// sequence of pushed traces.
func TestPayloadV04PreGrowWireIntegrity(t *testing.T) {
	for _, kb := range []int{1, 4, 16} {
		t.Run(itoaKB(kb), func(t *testing.T) {
			trace := mkTraceKB(kb)
			const pushCount = 10

			cold := newPayloadV04()
			warm := newPayloadV04()
			warm.buf.Grow(int(payloadSizeLimit) + trace.Msgsize()) // tightFit hint

			for range pushCount {
				_, err := cold.push(trace)
				require.NoError(t, err)
				_, err = warm.push(trace)
				require.NoError(t, err)
			}

			coldBytes, err := io.ReadAll(cold)
			require.NoError(t, err)
			warmBytes, err := io.ReadAll(warm)
			require.NoError(t, err)

			assert.Equal(t, coldBytes, warmBytes, "pre-grow must not change encoded wire bytes")
			assert.Equal(t, cold.itemCount(), warm.itemCount())
		})
	}
}

// TestPayloadV04HintConvergesAfterFlush verifies the key behavioral invariant
// of Approach B: when flush() passes oldp.size() as the hint to the next
// newPayloadV04, the replacement buffer must have enough pre-allocated capacity
// to absorb a full second fill cycle without any bytes.Buffer reallocation.
//
// This is tested at the payload level (no writer/config machinery needed)
// because newPayload(hint) for v04 calls p.grow(hint), which directly maps
// to bytes.Buffer.Grow. The writer-level wiring is covered by the build
// (newPayload signature change) and by TestPayloadV04HintEliminatesRampUpAllocs.
func TestPayloadV04HintConvergesAfterFlush(t *testing.T) {
	trace := mkTraceKB(2)
	limit := int(payloadSizeLimit)

	// Cycle 1: cold start — no hint, buffer ramps up naturally.
	p1 := newPayloadV04()
	assert.Equal(t, 0, p1.buf.Cap(), "cold-start buffer must have zero initial capacity")
	for p1.size() < limit {
		_, _ = p1.push(trace)
	}
	hint := p1.size() // what flush() passes to newPayload()

	// Cycle 2: apply the hint exactly as newPayload(hint) does for v04.
	p2 := newPayloadV04()
	p2.buf.Grow(hint)
	capAfterGrow := p2.buf.Cap()

	assert.GreaterOrEqual(t, capAfterGrow, hint,
		"pre-grown buffer must have capacity >= previous cycle's encoded size")

	// Fill cycle 2: the buffer must not reallocate — cap must stay constant.
	for p2.size() < limit {
		_, _ = p2.push(trace)
	}

	t.Logf("cycle1 encoded=%d hint=%d cycle2 cap before=%d after=%d",
		p1.size(), hint, capAfterGrow, p2.buf.Cap())
	assert.Equal(t, capAfterGrow, p2.buf.Cap(),
		"buffer cap must be stable throughout cycle 2: no reallocation expected")
}

// BenchmarkPayloadFillCycle benchmarks one complete fill cycle — from a fresh
// payloadV04 to payloadSizeLimit — under four pre-grow strategies. Use this to
// validate that tightFit (hint = payloadSizeLimit + one-trace headroom) matches
// or beats the current cold-start behaviour in both ns/op and B/op.
//
// Strategies:
//
//	current  — today's cold-start (no pre-grow)
//	atLimit  — Grow(payloadSizeLimit): naive, overshoots → one extra doubling
//	tightFit — Grow(payloadSizeLimit + Msgsize()): eliminates all doublings
//	maxLimit — Grow(payloadMaxLimit): guaranteed no regrowth, wastes ~5 MB
func BenchmarkPayloadFillCycle(b *testing.B) {
	for _, kb := range []int{1, 2, 8} {
		b.Run("current/"+itoaKB(kb), benchFillCycle(kb, pregrowNone))
		b.Run("atLimit/"+itoaKB(kb), benchFillCycle(kb, pregrowAtLimit))
		b.Run("tightFit/"+itoaKB(kb), benchFillCycle(kb, pregrowTightFit))
		b.Run("maxLimit/"+itoaKB(kb), benchFillCycle(kb, pregrowMaxLimit))
	}
}

// pregrowMode selects the pre-grow strategy under test.
type pregrowMode int

const (
	pregrowNone     pregrowMode = iota // current behavior: no pre-grow
	pregrowAtLimit                     // Grow(payloadSizeLimit) — naive
	pregrowTightFit                    // Grow(payloadSizeLimit + Msgsize()) — correct
	pregrowMaxLimit                    // Grow(payloadMaxLimit) — over-provisioned
)

func benchFillCycle(kb int, mode pregrowMode) func(*testing.B) {
	return func(b *testing.B) {
		trace := mkTraceKB(kb)
		limit := int(payloadSizeLimit)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			p := newPayloadV04()
			switch mode {
			case pregrowAtLimit:
				p.buf.Grow(limit)
			case pregrowTightFit:
				p.buf.Grow(limit + trace.Msgsize())
			case pregrowMaxLimit:
				p.buf.Grow(int(payloadMaxLimit))
			}
			for p.size() < limit {
				_, _ = p.push(trace)
			}
		}
	}
}

func itoaKB(kb int) string {
	switch kb {
	case 1:
		return "1KB"
	case 2:
		return "2KB"
	case 4:
		return "4KB"
	case 8:
		return "8KB"
	case 16:
		return "16KB"
	default:
		return "?KB"
	}
}
