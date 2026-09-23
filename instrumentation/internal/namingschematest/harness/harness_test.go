// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func TestLoadTracerConfigBeforeMockTracer(t *testing.T) {
	t.Cleanup(func() {
		require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
		tracer.Stop()
	})
	t.Setenv("DD_SERVICE", TestDDService)
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
	t.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", "true")

	loadTracerConfig(t, tracer.WithGlobalServiceName(true))

	i := instrumentation.Load(instrumentation.PackageNetHTTP)
	assert.True(t, i.OTelSemanticsEnabled())
	assert.Equal(t, "http.request", i.OperationName(instrumentation.ComponentServer, nil))
	assert.Equal(t, TestDDService, i.ServiceName(instrumentation.ComponentClient, nil))

	mt := mocktracer.Start()
	defer mt.Stop()
	span := tracer.StartSpan("test")
	span.Finish()
	require.Len(t, mt.FinishedSpans(), 1)
}
