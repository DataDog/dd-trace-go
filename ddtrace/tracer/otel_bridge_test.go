// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
)

// TestOTelBorrowNoOwner checks that a span whose context has no owner is not borrowed. The
// woven spanStart records the owner, so the test only runs in builds without Orchestrion.
func TestOTelBorrowNoOwner(t *testing.T) {
	if orchestrion.Enabled() {
		t.Skip("the woven spanStart sets the owner")
	}
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	s := StartSpan("op")
	defer s.Finish()
	ctx := ContextWithSpan(context.Background(), s)
	got, sc := borrowSpanFromContext(ctx, nil)
	assert.Nil(t, got)
	assert.Nil(t, sc)
}

// TestOTelBorrowExclude checks that the bridge skips the Datadog span that the caller names,
// and that a borrow pins the span context. A pinned context stays pinned.
func TestOTelBorrowExclude(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	s := StartSpan("op")
	defer s.Finish()
	spanContextWithOwner(s.Context(), s)
	ctx := ContextWithSpan(context.Background(), s)

	got, sc := borrowSpanFromContext(ctx, s)
	assert.Nil(t, got)
	assert.Nil(t, sc)

	got, sc = borrowSpanFromContext(ctx, nil)
	require.Same(t, s, got)
	require.Same(t, s.Context(), sc)
	assert.Equal(t, uint32(bridgePinned), s.Context().bridgeState.Load())

	// A second borrow of a pinned span also succeeds. The state stays pinned.
	got, sc = borrowSpanFromContext(ctx, nil)
	require.Same(t, s, got)
	require.Same(t, s.Context(), sc)
	assert.Equal(t, uint32(bridgePinned), s.Context().bridgeState.Load())
}

// TestOTelBorrowAfterRelease checks that a span that the pool released is not borrowed again.
func TestOTelBorrowAfterRelease(t *testing.T) {
	_, _, _, stop, err := startTestTracer(t)
	assert.NoError(t, err)
	defer stop()

	s := StartSpan("op")
	defer s.Finish()
	spanContextWithOwner(s.Context(), s)
	s.Context().bridgeState.Store(bridgeReleased)
	ctx := ContextWithSpan(context.Background(), s)

	got, sc := borrowSpanFromContext(ctx, nil)
	assert.Nil(t, got)
	assert.Nil(t, sc)
}

// The release tests below call releaseSpans on spans that they do not finish. A finished span
// goes to the tracer worker, which encodes and releases it at the same time as the test.

// TestOTelBorrowPinWins checks that a pinned span is not cleared and not returned to the span
// pool. The woven releaseSpans holds the compare-and-swap, so the test needs Orchestrion.
func TestOTelBorrowPinWins(t *testing.T) {
	if !orchestrion.Enabled() {
		t.Skip("needs Orchestrion")
	}
	_, _, _, stop, err := startTestTracer(t, WithSpanPool(true))
	assert.NoError(t, err)
	defer stop()

	s := StartSpan("op")
	ctx := ContextWithSpan(context.Background(), s)
	got, _ := borrowSpanFromContext(ctx, nil)
	require.Same(t, s, got)

	releaseSpans(true, []*Span{s})

	assert.NotEmpty(t, s.name, "clear must not run on a pinned span")
	assert.Equal(t, uint32(bridgePinned), s.Context().bridgeState.Load())
}

// TestOTelReleaseWins checks that a released span is cleared and not borrowed again. The
// woven releaseSpans sets the released state, so the test needs Orchestrion.
func TestOTelReleaseWins(t *testing.T) {
	if !orchestrion.Enabled() {
		t.Skip("needs Orchestrion")
	}
	_, _, _, stop, err := startTestTracer(t, WithSpanPool(true))
	assert.NoError(t, err)
	defer stop()

	s := StartSpan("op")
	ctx := ContextWithSpan(context.Background(), s)

	releaseSpans(true, []*Span{s})

	assert.Empty(t, s.name, "clear must run on an open span")
	assert.Equal(t, uint32(bridgeReleased), s.Context().bridgeState.Load())
	got, sc := borrowSpanFromContext(ctx, nil)
	assert.Nil(t, got)
	assert.Nil(t, sc)
}

// TestOTelBorrowReleaseRace checks that exactly one compare-and-swap wins between a borrow
// and a pool release, and that the winner defines the state of the span. The woven
// releaseSpans holds the compare-and-swap, so the test needs Orchestrion.
func TestOTelBorrowReleaseRace(t *testing.T) {
	if !orchestrion.Enabled() {
		t.Skip("needs Orchestrion")
	}
	_, _, _, stop, err := startTestTracer(t, WithSpanPool(true))
	assert.NoError(t, err)
	defer stop()

	for range 500 {
		s := StartSpan("op")
		ctx := ContextWithSpan(context.Background(), s)

		var (
			wg  sync.WaitGroup
			got *Span
		)
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			got, _ = borrowSpanFromContext(ctx, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			releaseSpans(true, []*Span{s})
		}()
		close(start)
		wg.Wait()

		if got != nil {
			assert.Equal(t, uint32(bridgePinned), s.Context().bridgeState.Load())
			assert.NotEmpty(t, s.name, "clear must not run on a pinned span")
		} else {
			assert.Equal(t, uint32(bridgeReleased), s.Context().bridgeState.Load())
		}
	}
}
