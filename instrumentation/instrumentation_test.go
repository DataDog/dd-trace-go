// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package instrumentation

import (
	"math"
	"os"
	"os/exec"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
	"github.com/stretchr/testify/require"
)

func TestInstrumentationStackTrace(t *testing.T) {
	instr := &Instrumentation{}

	captured := instr.CaptureStackTrace("stack-id", 0)
	require.True(t, captured.Valid())
	require.NotNil(t, captured.event)
	require.Equal(t, stacktrace.VulnerabilityEvent, captured.event.Category)
	require.Equal(t, "stack-id", captured.event.ID)
	require.Equal(t, "go", captured.event.Language)
	require.NotEmpty(t, captured.event.Frames)

	skipped := instr.CaptureStackTrace("stack-id", len(captured.event.Frames)+1)
	require.False(t, skipped.Valid())
	require.Nil(t, skipped.event)
	require.False(t, instr.CaptureStackTrace("", 0).Valid())
}

func TestInstrumentationRecordStackTraces(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	root := tracer.StartSpan("root")
	child := tracer.StartSpan("child", tracer.ChildOf(root.Context()))

	first := instr.CaptureStackTrace("first", 0)
	second := instr.CaptureStackTrace("second", 0)
	instr.RecordStackTraces(child, StackTrace{}, first, second)

	require.NotContains(t, child.AsMap(), stacktrace.SpanKey)
	value, ok := root.AsMap()[stacktrace.SpanKey]
	require.True(t, ok)
	eventsByCategory, ok := value.(map[string][]*stacktrace.Event)
	require.True(t, ok)
	require.Equal(t, []*stacktrace.Event{first.event, second.event}, eventsByCategory[string(stacktrace.VulnerabilityEvent)])

	third := instr.CaptureStackTrace("third", 0)
	instr.RecordStackTraces(child, third)
	eventsByCategory = root.AsMap()[stacktrace.SpanKey].(map[string][]*stacktrace.Event)
	require.Equal(t, []*stacktrace.Event{third.event}, eventsByCategory[string(stacktrace.VulnerabilityEvent)])
}

func TestInstrumentationStackTraceDisabled(t *testing.T) {
	if os.Getenv("DD_TEST_SUBPROCESS") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestInstrumentationStackTraceDisabled$")
		cmd.Env = append(os.Environ(),
			"DD_TEST_SUBPROCESS=1",
			"DD_APPSEC_STACK_TRACE_ENABLED=false",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		return
	}
	require.False(t, stacktrace.Enabled())

	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	span := tracer.StartSpan("span")
	captured := instr.CaptureStackTrace("stack-id", 0)
	instr.RecordStackTraces(span, StackTrace{event: &stacktrace.Event{}})

	require.False(t, captured.Valid())
	require.Nil(t, captured.event)
	require.NotContains(t, span.AsMap(), stacktrace.SpanKey)
}

func TestInstrumentationRecordStackTracesIgnoresEmptyInput(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	instr := &Instrumentation{}
	span := tracer.StartSpan("span")

	instr.RecordStackTraces(nil, instr.CaptureStackTrace("unused", 0))
	instr.RecordStackTraces(span)
	instr.RecordStackTraces(span, StackTrace{})

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
