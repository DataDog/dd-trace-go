// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCIVisibilityMockTracer_StartSpan_Routing verifies that spans are routed
// correctly based on their SpanType tag. CI Visibility spans should go to the
// real tracer, others to the mock tracer.
func TestCIVisibilityMockTracer_StartSpan_Routing(t *testing.T) {
	// Note: The 'real' tracer here will be the default global tracer.
	// If mocktracer.Start() was called *before* this, 'real' would also be a mock.
	// We rely on the fact that CI spans won't appear in the *internal* mock tracer (`cmt.mock`).
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	defer internal.SetGlobalTracer(cmt.real)

	// 1. Regular span (should go to internal mock)
	regSpan := cmt.StartSpan("regular.op")
	require.NotNil(t, regSpan)
	regSpan.Finish()

	// 2. CI Visibility span (should go to real tracer)
	ciSpan := cmt.StartSpan("ci.test.op", tracer.SpanType(constants.SpanTypeTest))
	// We might not have a real tracer configured to actually *do* anything,
	// but the key is it *shouldn't* be handled by cmt.mock.
	// If ciSpan is nil, it means the real tracer is likely a NoopTracer, which is fine for this test.
	if ciSpan != nil {
		ciSpan.Finish() // Finish it if we got one
	}

	// Verification
	mockedSpans := cmt.mock.FinishedSpans() // Access internal mock directly for verification
	assert.Len(t, mockedSpans, 1, "Only the regular span should be in the internal mock tracer")
	if len(mockedSpans) == 1 {
		assert.Equal(t, "regular.op", mockedSpans[0].OperationName())
		assert.NotEqual(t, "ci.test.op", mockedSpans[0].OperationName())
	}

	// Check the public FinishedSpans() method also reflects the internal mock
	publicFinished := cmt.FinishedSpans()
	assert.Len(t, publicFinished, 1, "Public FinishedSpans should match internal mock")
	if len(publicFinished) == 1 {
		assert.Equal(t, "regular.op", publicFinished[0].OperationName())
	}

	// Check OpenSpans - should be empty now
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after finishing")
}

// TestCIVisibilityMockTracer_Delegation verifies basic delegation methods.
func TestCIVisibilityMockTracer_Delegation(t *testing.T) {
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	defer internal.SetGlobalTracer(cmt.real)

	// Test Reset
	span1 := cmt.StartSpan("op1")
	span1.Finish()
	assert.Len(t, cmt.FinishedSpans(), 1)
	cmt.Reset()
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should be empty after Reset")
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after Reset")

	// Test Open/Finished Spans sequence
	span2 := cmt.StartSpan("op2")
	assert.Len(t, cmt.OpenSpans(), 1, "Should have 1 open span")
	assert.Equal(t, "op2", cmt.OpenSpans()[0].OperationName())
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should be empty while span is open")

	span2.Finish()
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after finish")
	assert.Len(t, cmt.FinishedSpans(), 1, "Should have 1 finished span")
	assert.Equal(t, "op2", cmt.FinishedSpans()[0].OperationName())
}

// TestCIVisibilityMockTracer_Stop verifies that the tracer becomes no-op after Stop.
func TestCIVisibilityMockTracer_Stop(t *testing.T) {
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	defer internal.SetGlobalTracer(cmt.real)

	cmt.Stop() // Stop the tracer

	// Verify isnoop is set (internal check, not strictly necessary but good for understanding)
	assert.True(t, cmt.isnoop.Load(), "isnoop flag should be true after Stop")

	// Verify methods become no-op
	assert.Nil(t, cmt.StartSpan("op.after.stop"), "StartSpan should return nil after Stop")

	ctx, err := cmt.Extract(http.Header{})
	assert.Nil(t, ctx, "Extract should return nil context after Stop")
	assert.NoError(t, err, "Extract should return no error after Stop")

	err = cmt.Inject(nil, http.Header{})
	assert.NoError(t, err, "Inject should return no error after Stop")

	assert.Nil(t, cmt.GetDataStreamsProcessor(), "GetDataStreamsProcessor should return nil after Stop")
	assert.Nil(t, cmt.SentDSMBacklogs(), "SentDSMBacklogs should return nil after Stop")

	// Check span lists are not affected (though Reset would clear them)
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should remain empty")
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should remain empty")
}

// TestCIVisibilityMockTracer_Flush verifies that Flush moves open spans to finished.
func TestCIVisibilityMockTracer_Flush(t *testing.T) {
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	defer internal.SetGlobalTracer(cmt.real)

	// Start a regular span (handled by internal mock) but don't finish it
	s := cmt.StartSpan("span.to.flush")
	require.NotNil(t, s)

	// Verify it's in OpenSpans
	open := cmt.OpenSpans()
	require.Len(t, open, 1)
	assert.Equal(t, s.Context().SpanID(), open[0].Context().SpanID())
	assert.Empty(t, cmt.FinishedSpans())

	// Call Flush
	cmt.Flush() // Should flush both mock and real (though we only check mock here)

	// Verify the span moved from Open to Finished in the mock tracer
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after Flush")
	finished := cmt.FinishedSpans()
	require.Len(t, finished, 1)
	assert.Equal(t, s.Context().SpanID(), finished[0].Context().SpanID())
	assert.Equal(t, "span.to.flush", finished[0].OperationName())
}

// TestCIVisibilityMockTracer_TracerConf verifies TracerConf delegates correctly.
func TestCIVisibilityMockTracer_TracerConf(t *testing.T) {
	cmt := newCIVisibilityMockTracer()
	defer cmt.Stop()

	conf := cmt.TracerConf()
	// The default mock tracer has an empty config, so we check that
	assert.Equal(t, tracer.TracerConf{}, conf)
}

// TestCIVisibilityMockTracer_SentDSMBacklogs tests DSM backlog retrieval.
func TestCIVisibilityMockTracer_SentDSMBacklogs(t *testing.T) {
	cmt := newCIVisibilityMockTracer()
	defer cmt.Stop()

	// Initially, no backlogs
	backlogs := cmt.SentDSMBacklogs()
	assert.Empty(t, backlogs)

	// Simulate some DSM activity (indirectly, as direct simulation is complex)
	// For now, we know the mockDSMTransport starts empty, and flushing doesn't add
	// without pathway activity, so this test mainly ensures the method doesn't panic
	// and returns the expected (empty) list from the internal mock transport.
	cmt.Flush() // Flush includes DSM flush

	backlogs = cmt.SentDSMBacklogs() // Flushes again internally
	assert.Empty(t, backlogs)        // Still expect empty unless DSM was used

	// Test after stop
	cmt.Stop()
	assert.Nil(t, cmt.SentDSMBacklogs(), "Should return nil after stop")
}
