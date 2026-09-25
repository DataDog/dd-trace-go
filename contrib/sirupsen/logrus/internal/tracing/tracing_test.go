// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracing

import (
	"context"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

func TestInjectTraceFields(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, ctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	fields := map[string]any{}
	InjectTraceFields(ctx, fields)

	// By default, trace IDs are logged in 128bit format
	assert.Equal(t, sp.Context().TraceID(), fields["dd.trace_id"])
	assert.Equal(t, "1234", fields["dd.span_id"])
}

func TestInjectTraceFields128BitDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	// cfg is read at package init, before t.Setenv runs.
	cfg = newConfig()
	t.Cleanup(func() { cfg = newConfig() })

	tracer.Start()
	defer tracer.Stop()
	sp, ctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	fields := map[string]any{}
	InjectTraceFields(ctx, fields)

	assert.Equal(t, strconv.FormatUint(sp.Context().TraceIDLower(), 10), fields["dd.trace_id"])
	assert.Equal(t, "1234", fields["dd.span_id"])
}

func TestInjectTraceFieldsNoSpan(t *testing.T) {
	fields := map[string]any{}
	InjectTraceFields(context.Background(), fields)
	assert.Empty(t, fields)
}
