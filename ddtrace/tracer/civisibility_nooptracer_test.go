// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCiVisibilityNoopTracerImplementsTracer(t *testing.T) {
	// Verify that CiVisibilityNoopTracer implements the Tracer interface
	var _ Tracer = (*CiVisibilityNoopTracer)(nil)
}

func TestWrapWithCiVisibilityNoopTracer(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)
	assert.NotNil(t, wrapped)
	assert.Equal(t, tr, wrapped.Tracer)
}

func TestCiVisibilityNoopTracer_StartSpan_CIVisibilitySpanTypes(t *testing.T) {
	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	testCases := []struct {
		name     string
		spanType string
	}{
		{"test span", constants.SpanTypeTest},
		{"test suite span", constants.SpanTypeTestSuite},
		{"test module span", constants.SpanTypeTestModule},
		{"test session span", constants.SpanTypeTestSession},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			span := wrapped.StartSpan("test.operation",
				SpanType(tc.spanType),
				ResourceName("test-resource"),
			)

			// CI Visibility spans should be created
			require.NotNil(t, span, "expected span to be created for span type %s", tc.spanType)

			// Verify the span has the correct type using AsMap()
			assert.Equal(t, tc.spanType, span.AsMap()[ext.SpanType])

			span.Finish()
		})
	}

	// Flush and verify all spans were sent
	flush(len(testCases))
	assert.Equal(t, len(testCases), transport.Len())
}

func TestCiVisibilityNoopTracer_StartSpan_NonCIVisibilitySpanTypes(t *testing.T) {
	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	testCases := []struct {
		name     string
		spanType string
	}{
		{"web span", "web"},
		{"db span", "db"},
		{"cache span", "cache"},
		{"http span", "http"},
		{"custom span", "custom"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			span := wrapped.StartSpan("test.operation",
				SpanType(tc.spanType),
				ResourceName("test-resource"),
			)

			// Non-CI Visibility spans should return nil
			assert.Nil(t, span, "expected nil span for non-CI Visibility span type %s", tc.spanType)
		})
	}

	// No spans should be sent since all were no-op
	flush(-1)
	assert.Equal(t, 0, transport.Len())
}

func TestCiVisibilityNoopTracer_StartSpan_NoOptions(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// StartSpan with no options should return nil (noop behavior)
	span := wrapped.StartSpan("test.operation")
	assert.Nil(t, span)
}

func TestCiVisibilityNoopTracer_StartSpan_NoSpanType(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// StartSpan without SpanType should return nil (noop behavior)
	span := wrapped.StartSpan("test.operation",
		ResourceName("test-resource"),
		Tag("custom.tag", "value"),
	)
	assert.Nil(t, span)
}

func TestCiVisibilityNoopTracer_StartSpan_PreservesConfig(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Create a parent span first
	parentSpan := tr.StartSpan("parent.operation", SpanType(constants.SpanTypeTestSession))
	require.NotNil(t, parentSpan)

	startTime := time.Now().Add(-time.Hour)

	// Create a CI Visibility span with various options
	span := wrapped.StartSpan("test.operation",
		SpanType(constants.SpanTypeTest),
		ResourceName("test-resource"),
		Tag("custom.tag", "custom-value"),
		StartTime(startTime),
		ChildOf(parentSpan.Context()),
	)

	require.NotNil(t, span)

	// Verify all configurations are preserved
	assert.Equal(t, constants.SpanTypeTest, span.AsMap()[ext.SpanType])
	assert.Equal(t, "custom-value", span.AsMap()["custom.tag"])
	assert.Equal(t, startTime.UnixNano(), span.start)
	assert.Equal(t, parentSpan.context.spanID, span.parentID)

	span.Finish()
	parentSpan.Finish()
}

func TestCiVisibilityNoopTracer_SetServiceInfo(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// SetServiceInfo should be a no-op and not panic
	assert.NotPanics(t, func() {
		wrapped.SetServiceInfo("service", "app", "type")
	})
}

func TestCiVisibilityNoopTracer_Extract(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Extract should always return nil, nil
	carrier := TextMapCarrier(map[string]string{
		"x-datadog-trace-id":  "123",
		"x-datadog-parent-id": "456",
	})

	ctx, err := wrapped.Extract(carrier)
	assert.Nil(t, ctx)
	assert.Nil(t, err)
}

func TestCiVisibilityNoopTracer_Inject(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Create a span context to inject
	span := tr.StartSpan("test.operation", SpanType(constants.SpanTypeTest))
	require.NotNil(t, span)

	carrier := TextMapCarrier(map[string]string{})

	// Inject should return nil (no error) but not actually inject anything
	injectErr := wrapped.Inject(span.Context(), carrier)
	assert.Nil(t, injectErr)
	assert.Empty(t, carrier)

	span.Finish()
}

func TestCiVisibilityNoopTracer_Stop(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Stop should forward to the wrapped tracer and not panic
	assert.NotPanics(t, func() {
		wrapped.Stop()
	})
}

func TestCiVisibilityNoopTracer_TracerConf(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// TracerConf should return the same config as the wrapped tracer
	wrappedConf := wrapped.TracerConf()
	tracerConf := tr.TracerConf()

	assert.Equal(t, tracerConf, wrappedConf)
}

func TestCiVisibilityNoopTracer_Flush(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Flush should forward to the wrapped tracer and not panic
	assert.NotPanics(t, func() {
		wrapped.Flush()
	})
}

func TestUseConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		opt := useConfig(nil)
		cfg := &StartSpanConfig{}

		// Should not panic and should not modify cfg
		assert.NotPanics(t, func() {
			opt(cfg)
		})
		assert.Nil(t, cfg.Parent)
		assert.True(t, cfg.StartTime.IsZero())
		assert.Nil(t, cfg.Tags)
	})

	t.Run("full config", func(t *testing.T) {
		startTime := time.Now()
		parent := &SpanContext{}
		tags := map[string]interface{}{
			"key": "value",
		}
		spanLinks := []SpanLink{
			{TraceID: 123, SpanID: 456},
		}

		srcCfg := &StartSpanConfig{
			Parent:    parent,
			StartTime: startTime,
			Tags:      tags,
			SpanID:    999,
			SpanLinks: spanLinks,
		}

		opt := useConfig(srcCfg)
		dstCfg := &StartSpanConfig{}
		opt(dstCfg)

		assert.Equal(t, parent, dstCfg.Parent)
		assert.Equal(t, startTime, dstCfg.StartTime)
		assert.Equal(t, tags, dstCfg.Tags)
		assert.Equal(t, uint64(999), dstCfg.SpanID)
		assert.Equal(t, spanLinks, dstCfg.SpanLinks)
	})
}

func TestCiVisibilityNoopTracer_StartSpan_EmptyOptions(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// StartSpan with empty slice of options should return nil
	span := wrapped.StartSpan("test.operation", []StartSpanOption{}...)
	assert.Nil(t, span)
}

func TestCiVisibilityNoopTracer_StartSpan_AllCIVisibilityTypes(t *testing.T) {
	// Test that all CI Visibility span types are properly recognized
	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Create one of each CI Visibility span type
	testSpan := wrapped.StartSpan("test.span", SpanType(constants.SpanTypeTest))
	suiteSpan := wrapped.StartSpan("suite.span", SpanType(constants.SpanTypeTestSuite))
	moduleSpan := wrapped.StartSpan("module.span", SpanType(constants.SpanTypeTestModule))
	sessionSpan := wrapped.StartSpan("session.span", SpanType(constants.SpanTypeTestSession))

	// All should be non-nil
	require.NotNil(t, testSpan)
	require.NotNil(t, suiteSpan)
	require.NotNil(t, moduleSpan)
	require.NotNil(t, sessionSpan)

	// Finish all spans
	testSpan.Finish()
	suiteSpan.Finish()
	moduleSpan.Finish()
	sessionSpan.Finish()

	// Flush and verify
	flush(4)
	assert.Equal(t, 4, transport.Len())
}

func TestCiVisibilityNoopTracer_MixedSpans(t *testing.T) {
	// Test that CI Visibility spans work while non-CI spans are dropped
	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Create CI Visibility spans (should work)
	ciSpan1 := wrapped.StartSpan("ci.span1", SpanType(constants.SpanTypeTest))
	ciSpan2 := wrapped.StartSpan("ci.span2", SpanType(constants.SpanTypeTestSuite))

	// Create non-CI spans (should be nil)
	nonCISpan1 := wrapped.StartSpan("non.ci.span1", SpanType("web"))
	nonCISpan2 := wrapped.StartSpan("non.ci.span2", SpanType("db"))
	nonCISpan3 := wrapped.StartSpan("non.ci.span3") // no span type

	// Verify CI spans are created
	require.NotNil(t, ciSpan1)
	require.NotNil(t, ciSpan2)

	// Verify non-CI spans are nil
	assert.Nil(t, nonCISpan1)
	assert.Nil(t, nonCISpan2)
	assert.Nil(t, nonCISpan3)

	// Finish CI spans
	ciSpan1.Finish()
	ciSpan2.Finish()

	// Only CI spans should be flushed
	flush(2)
	assert.Equal(t, 2, transport.Len())
}

func TestUseConfig_WithContext(t *testing.T) {
	// Test that useConfig properly copies the Context field
	ctx := context.Background()

	srcCfg := &StartSpanConfig{
		Context: ctx,
	}

	opt := useConfig(srcCfg)
	dstCfg := &StartSpanConfig{}
	opt(dstCfg)

	assert.Equal(t, ctx, dstCfg.Context)
}

func TestCiVisibilityNoopTracer_StartSpan_SpanTypeSpanIsNotCIVisibility(t *testing.T) {
	// Test that SpanTypeSpan (which is "span") is NOT considered a CI Visibility span type
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// SpanTypeSpan ("span") should be treated as non-CI Visibility
	span := wrapped.StartSpan("test.operation", SpanType(constants.SpanTypeSpan))
	assert.Nil(t, span, "expected nil span for SpanTypeSpan")
}

func TestCiVisibilityNoopTracer_StartSpan_WithSpanLinks(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	spanLinks := []SpanLink{
		{TraceID: 123, TraceIDHigh: 0, SpanID: 456},
		{TraceID: 789, TraceIDHigh: 0, SpanID: 101},
	}

	span := wrapped.StartSpan("test.operation",
		SpanType(constants.SpanTypeTest),
		WithSpanLinks(spanLinks),
	)

	require.NotNil(t, span)
	// The span links should have been passed through via useConfig
	span.Finish()
}

func TestCiVisibilityNoopTracer_ChildSpanFiltering(t *testing.T) {
	// Test that child spans of CI Visibility spans are also filtered (non-CI children return nil)
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := WrapWithCiVisibilityNoopTracer(tr)

	// Create a CI Visibility parent span
	parentSpan := wrapped.StartSpan("parent.operation", SpanType(constants.SpanTypeTest))
	require.NotNil(t, parentSpan)

	// Try to create a non-CI Visibility child span - should return nil
	// because the CiVisibilityNoopTracer filters based on span type, not parent relationship
	childSpan := wrapped.StartSpan("child.operation",
		ChildOf(parentSpan.Context()),
		SpanType("web"),
	)
	assert.Nil(t, childSpan, "non-CI child spans should return nil")

	// Create a CI Visibility child span - should work
	ciChildSpan := wrapped.StartSpan("ci.child.operation",
		ChildOf(parentSpan.Context()),
		SpanType(constants.SpanTypeTest),
	)
	require.NotNil(t, ciChildSpan)

	ciChildSpan.Finish()
	parentSpan.Finish()
}
