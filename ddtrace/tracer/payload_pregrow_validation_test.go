// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

// Tests validating the adaptive pre-grow: agentTraceWriter passes the previous
// flush cycle's encoded size as a hint so the next payload's buffer is
// right-sized on first push instead of growing incrementally.

import (
	"io"
	"strconv"
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

// TestPayloadV04PreGrowWireIntegrity verifies that grow() does not change the
// encoded wire bytes, comparing a cold payload against one pre-grown via the
// same grow() method the writer uses in production.
func TestPayloadV04PreGrowWireIntegrity(t *testing.T) {
	for _, kb := range []int{1, 4, 16} {
		t.Run(strconv.Itoa(kb)+"KB", func(t *testing.T) {
			trace := mkTraceKB(kb)
			const pushCount = 10

			cold := newPayloadV04()
			warm := newPayloadV04()
			warm.grow(int(payloadSizeLimit) + trace.Msgsize())

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

			assert.Equal(t, coldBytes, warmBytes, "grow() must not change encoded wire bytes")
			assert.Equal(t, cold.itemCount(), warm.itemCount())
		})
	}
}

// TestPayloadV04HintConvergesAfterFlush verifies grow() defers allocation
// until the first push (no idle memory pinning) and that the hint is then
// applied so the fill cycle completes without reallocating.
func TestPayloadV04HintConvergesAfterFlush(t *testing.T) {
	trace := mkTraceKB(2)
	limit := int(payloadSizeLimit)

	p1 := newPayloadV04()
	assert.Equal(t, 0, p1.buf.Cap(), "cold-start buffer must have zero initial capacity")
	for p1.size() < limit {
		_, _ = p1.push(trace)
	}
	hint := p1.size()

	p2 := newPayloadV04()
	p2.grow(hint)
	assert.Equal(t, 0, p2.buf.Cap(), "grow() must not allocate before first push")

	_, _ = p2.push(trace)
	capAfterFirstPush := p2.buf.Cap()
	assert.GreaterOrEqual(t, capAfterFirstPush, hint, "first push must apply the hint")

	for p2.size() < limit {
		_, _ = p2.push(trace)
	}
	assert.Equal(t, capAfterFirstPush, p2.buf.Cap(), "buffer cap must be stable: no reallocation expected")
}

// mkRepeatedTrace returns a spanList with repeated small strings so the v1
// string table compacts across pushes, matching a realistic steady-state size.
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
		spans[i] = s
	}
	return spans
}

// TestPayloadV1PreGrowWireIntegrity verifies that sizeHint does not change the
// encoded wire bytes: a reference (unhinted) payload must match one with
// sizeHint set, independent of the pre-grow mechanism itself.
//
// Uses newPayloadV1() directly (not the pool) so both payloads start from
// identical zero state, and a single-tag trace so span.meta map iteration is
// deterministic.
func TestPayloadV1PreGrowWireIntegrity(t *testing.T) {
	s := newBasicSpan("http.request")
	s.start = fixedTime
	s.service = "my-service"
	s.meta.Set("env", "production")
	trace := spanList{s}

	const pushCount = 10
	reference := newPayloadV1()
	hinted := newPayloadV1()
	hinted.sizeHint = int(payloadSizeLimit)

	for range pushCount {
		_, err := reference.push(trace)
		require.NoError(t, err)
		_, err = hinted.push(trace)
		require.NoError(t, err)
	}

	referenceBytes, err := io.ReadAll(reference)
	require.NoError(t, err)
	hintedBytes, err := io.ReadAll(hinted)
	require.NoError(t, err)

	assert.Equal(t, referenceBytes, hintedBytes, "sizeHint must not change encoded wire bytes")
	assert.Equal(t, reference.itemCount(), hinted.itemCount())
}

// TestPayloadV1ClearDiscardRetain documents the maxRetainedBufCap threshold:
// buffers above 1 MB are discarded by clear() (the hot path at full
// payloads), buffers below are retained.
func TestPayloadV1ClearDiscardRetain(t *testing.T) {
	large := getPayloadV1()
	trace := mkRepeatedTrace(5)
	for large.size() < maxRetainedBufCap+1 {
		_, _ = large.push(trace)
	}
	assert.Greater(t, cap(large.buf), maxRetainedBufCap, "sanity: buf grew past cap threshold")
	large.clear()
	assert.Equal(t, 0, cap(large.buf), "buf must be discarded when cap > maxRetainedBufCap")
	putPayloadV1(large)

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
