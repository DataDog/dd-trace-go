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

// TestPayloadV04HintConvergesAfterFlush verifies the lazy pre-grow behavior:
// grow() must not allocate before the first push (avoiding idle memory pinning),
// and the hint must be applied on first push so the fill cycle completes without
// reallocation.
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

	// Cycle 2: apply the hint via grow() as newPayload(hint) does for v04.
	// grow() must NOT allocate eagerly — cap stays zero until first push.
	p2 := newPayloadV04()
	p2.grow(hint)
	assert.Equal(t, 0, p2.buf.Cap(),
		"grow() must not allocate before first push (lazy hint)")

	// First push: hint is applied — capacity must jump to >= hint.
	_, _ = p2.push(trace)
	capAfterFirstPush := p2.buf.Cap()
	assert.GreaterOrEqual(t, capAfterFirstPush, hint,
		"first push must apply the hint: capacity must be >= previous cycle's encoded size")

	// Fill cycle 2: the buffer must not reallocate — cap must stay constant.
	for p2.size() < limit {
		_, _ = p2.push(trace)
	}

	t.Logf("cycle1 encoded=%d hint=%d cycle2 cap after-first-push=%d after-fill=%d",
		p1.size(), hint, capAfterFirstPush, p2.buf.Cap())
	assert.Equal(t, capAfterFirstPush, p2.buf.Cap(),
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

// mkRepeatedTrace returns a spanList with repeated small strings so the v1
// string table actually compacts across pushes, producing sizes that represent
// a realistic steady-state workload for the hint heuristic.
func mkRepeatedTrace(numSpans int) spanList {
	spans := make(spanList, numSpans)
	for i := range numSpans {
		s := newBasicSpan("http.request")
		s.start = fixedTime
		s.service = "my-service"
		s.resource = "GET /api/v1/users"
		s.meta.Set("env", "production")
		s.meta.Set("version", "1.0.0")
		s.meta.Set("span.kind", "server")
		s.meta.Set("http.method", "GET")
		s.meta.Set("http.status_code", "200")
		_ = i
		spans[i] = s
	}
	return spans
}

// TestPayloadV1PreGrowWireIntegrity verifies that setting sizeHint on a v1
// payload does not change the encoded wire bytes. A cold payload and a
// hint-pre-sized payload must produce byte-identical output for the same
// sequence of pushed traces.
//
// Uses newPayloadV1() directly (not the pool) so both payloads start from
// identical zero state — pool objects carry cached string tables and process
// tag state across calls that would make byte comparison unreliable.
//
// Uses a single-tag trace so span.meta map iteration is deterministic
// (Go randomises multi-entry map order, which would make byte comparison
// unreliable even though the content is correct).
func TestPayloadV1PreGrowWireIntegrity(t *testing.T) {
	s := newBasicSpan("http.request")
	s.start = fixedTime
	s.service = "my-service"
	s.meta.Set("env", "production") // single tag → deterministic map iteration
	trace := spanList{s}

	const pushCount = 10
	cold := newPayloadV1()
	warm := newPayloadV1()
	warm.sizeHint = int(payloadSizeLimit)

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

	assert.Equal(t, coldBytes, warmBytes, "sizeHint must not change encoded wire bytes")
	assert.Equal(t, cold.itemCount(), warm.itemCount())
}

// TestPayloadV1ClearDiscardRetain documents the maxRetainedBufCap threshold:
// buffers above 1 MB are discarded by clear() (the hot path at full payloads),
// buffers below are retained. This confirms that the pre-grow + discard pattern
// is always in play for payloads that approach payloadSizeLimit.
func TestPayloadV1ClearDiscardRetain(t *testing.T) {
	// Case 1: fill above maxRetainedBufCap — buffer must be discarded on clear.
	large := getPayloadV1()
	trace := mkRepeatedTrace(5)
	for large.size() < maxRetainedBufCap+1 {
		_, _ = large.push(trace)
	}
	assert.Greater(t, cap(large.buf), maxRetainedBufCap, "sanity: buf grew past cap threshold")
	large.clear()
	assert.Equal(t, 0, cap(large.buf), "buf must be discarded when cap > maxRetainedBufCap")
	putPayloadV1(large)

	// Case 2: fill below maxRetainedBufCap — buffer must be retained (len=0, cap>0).
	small := getPayloadV1()
	_, _ = small.push(mkRepeatedTrace(1))
	capBefore := cap(small.buf)
	assert.Greater(t, capBefore, 0, "sanity: buf must have grown after a push")
	assert.LessOrEqual(t, capBefore, maxRetainedBufCap, "sanity: small payload stays under cap threshold")
	small.clear()
	assert.Equal(t, capBefore, cap(small.buf), "buf cap must be retained when cap <= maxRetainedBufCap")
	assert.Equal(t, 0, len(small.buf), "buf len must be reset to 0 on clear")
	putPayloadV1(small)
}

// BenchmarkPayloadV1FillCycle benchmarks one complete v1 fill cycle under two
// strategies: cold-start (no hint, ramps via append doubling) and tightFit
// (sizeHint = previous cycle's real compacted size). Unlike the v04 benchmark,
// this exercises the sync.Pool path (getPayloadV1 / putPayloadV1 each iter) and
// uses a repeated-string trace so the string table compaction is representative.
//
// Note: payloadSizeLimit > maxRetainedBufCap (4.75 MB > 1 MB), so clear() always
// discards the buffer on the hot path. The hint eliminates the per-cycle
// ramp-up that discard forces.
func BenchmarkPayloadV1FillCycle(b *testing.B) {
	trace := mkRepeatedTrace(5)
	limit := int(payloadSizeLimit)

	// Warm-up cycle to get the real compacted size as hint.
	p0 := getPayloadV1()
	for p0.size() < limit {
		_, _ = p0.push(trace)
	}
	hint := p0.size()
	putPayloadV1(p0)

	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			p := getPayloadV1()
			for p.size() < limit {
				_, _ = p.push(trace)
			}
			putPayloadV1(p)
		}
	})

	b.Run("tightFit", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			p := getPayloadV1()
			p.sizeHint = hint
			for p.size() < limit {
				_, _ = p.push(trace)
			}
			putPayloadV1(p)
		}
	})
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
