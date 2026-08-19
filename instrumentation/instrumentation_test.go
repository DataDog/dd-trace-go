// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
)

func TestOTelSemanticsEnabled(t *testing.T) {
	t.Cleanup(func() { internalconfig.CreateNew() })
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")
	internalconfig.CreateNew()

	require.True(t, new(Instrumentation).OTelSemanticsEnabled())
}

func TestInstrumentationStackTrace(t *testing.T) {
	instr := &Instrumentation{}

	for _, category := range []StackTraceCategory{
		StackTraceCategoryException,
		StackTraceCategoryVulnerability,
		StackTraceCategoryExploit,
	} {
		captured := instr.CaptureStackTrace(
			category,
			WithStackTraceID("stack-id"),
			WithStackTraceType("event-type"),
			WithStackTraceMessage("message"),
			WithStackTraceDepth(2),
		)
		require.NotNil(t, captured)
		require.Equal(t, category, captured.Category)
		require.Equal(t, "stack-id", captured.ID)
		require.Equal(t, "event-type", captured.Type)
		require.Equal(t, "message", captured.Message)
		require.Equal(t, "go", captured.Language)
		require.NotEmpty(t, captured.Frames)
		require.LessOrEqual(t, len(captured.Frames), 2)
	}

	require.NotNil(t, instr.CaptureStackTrace(StackTraceCategoryException))
	require.Nil(t, instr.CaptureStackTrace(StackTraceCategoryVulnerability))
	require.Nil(t, instr.CaptureStackTrace(StackTraceCategoryExploit))
	require.NotNil(t, instr.CaptureStackTrace(
		StackTraceCategoryExploit,
		WithStackTraceID("stack-id"),
		WithStackTraceDepth(-1),
	))
	require.Nil(t, instr.CaptureStackTrace(StackTraceCategoryException, WithStackTraceSkip(1_000)))
}

func TestInstrumentationRecordStackTrace(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	root := tracer.StartSpan("root")
	child := tracer.StartSpan("child", tracer.ChildOf(root.Context()))

	first := instr.CaptureStackTrace(StackTraceCategoryVulnerability, WithStackTraceID("first"))
	second := instr.CaptureStackTrace(StackTraceCategoryVulnerability, WithStackTraceID("second"))
	instr.RecordStackTrace(child, first)
	instr.RecordStackTrace(child, second)

	require.NotContains(t, child.AsMap(), stacktrace.SpanKey)
	value, ok := root.AsMap()[stacktrace.SpanKey]
	require.True(t, ok)
	eventsByCategory, ok := value.(map[string][]*stacktrace.Event)
	require.True(t, ok)
	require.Equal(t, []*stacktrace.Event{first, second}, eventsByCategory[string(stacktrace.VulnerabilityEvent)])

	exploit := stacktrace.NewEvent(stacktrace.ExploitEvent, stacktrace.WithID("exploit"))
	stacktrace.AddToSpan(root, exploit)
	third := instr.CaptureStackTrace(StackTraceCategoryVulnerability, WithStackTraceID("third"))
	instr.RecordStackTrace(child, third)

	eventsByCategory = root.AsMap()[stacktrace.SpanKey].(map[string][]*stacktrace.Event)
	require.Equal(t, []*stacktrace.Event{first, second, third}, eventsByCategory[string(stacktrace.VulnerabilityEvent)])
	require.Equal(t, []*stacktrace.Event{exploit}, eventsByCategory[string(stacktrace.ExploitEvent)])
}

func TestInstrumentationRecordStackTraceConcurrent(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	span := tracer.StartSpan("span")
	const count = 32
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			instr.RecordStackTrace(span, &StackTrace{
				Category: StackTraceCategoryVulnerability,
				ID:       strconv.Itoa(i),
				Frames:   stacktrace.StackTrace{{}},
			})
		})
	}
	wg.Wait()

	eventsByCategory := span.AsMap()[stacktrace.SpanKey].(map[string][]*stacktrace.Event)
	require.Len(t, eventsByCategory[string(stacktrace.VulnerabilityEvent)], count)
}

func TestInstrumentationRecordStackTraceIgnoresInvalidInput(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	span := tracer.StartSpan("span")

	require.False(t, instr.RecordStackTrace(nil, instr.CaptureStackTrace(StackTraceCategoryException)))
	require.False(t, instr.RecordStackTrace(span, nil))
	require.False(t, instr.RecordStackTrace(span, &StackTrace{}))
	require.False(t, instr.RecordStackTrace(span, &StackTrace{
		Category: StackTraceCategoryVulnerability,
		Frames:   stacktrace.StackTrace{{}},
	}))
	require.False(t, instr.RecordStackTrace(span, &StackTrace{
		Category: StackTraceCategoryExploit,
		Frames:   stacktrace.StackTrace{{}},
	}))

	require.NotContains(t, span.AsMap(), stacktrace.SpanKey)
}

func TestInstrumentation_AnalyticsRate(t *testing.T) {
	pkgs := GetPackages()
	for pkg, info := range pkgs {
		t.Run(string(pkg), func(t *testing.T) {
			// Skip packages that don't implement analytics functionality
			if pkg == PackageAWSDatadogLambdaGo {
				t.Skip("Lambda contrib does not implement analytics functionality")
				return
			}

			instr := Load(pkg)

			// No env var set, without defaulting to global should return NaN
			rate := instr.AnalyticsRate(false)
			require.True(t, math.IsNaN(rate))

			// With env var set, should return 1.0
			t.Setenv("DD_TRACE_"+info.EnvVarPrefix+"_ANALYTICS_ENABLED", "true")
			rate = instr.AnalyticsRate(false)
			require.Equal(t, 1.0, rate)
		})
	}
}
