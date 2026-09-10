// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package zap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

func newJSONCore(buf *bytes.Buffer) zapcore.Core {
	return zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(buf),
		zapcore.DebugLevel,
	)
}

// runTest starts a root span, calls logFn with the context, then asserts every
// log line contains dd.trace_id and dd.span_id matching the active span exactly.
func runTest(ctx context.Context, t *testing.T, buf *bytes.Buffer, logFn func(context.Context)) {
	t.Helper()

	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	logFn(ctx)

	wantTraceID := span.Context().TraceID()
	wantSpanID := strconv.FormatUint(span.Context().SpanID(), 10)

	logs := buf.String()
	t.Logf("got logs:\n%s", logs)
	require.NotEmpty(t, logs, "no log output produced")

	s := bufio.NewScanner(bytes.NewBufferString(logs))
	for s.Scan() {
		line := s.Bytes()
		var data map[string]any
		require.NoError(t, json.Unmarshal(line, &data), "log line is not valid JSON: %s", line)
		assert.Equal(t, wantTraceID, data["dd.trace_id"], "wrong dd.trace_id in: %s", line)
		assert.Equal(t, wantSpanID, data["dd.span_id"], "wrong dd.span_id in: %s", line)
	}
}

func expectedTraces() trace.Traces { return trace.Traces{} }
