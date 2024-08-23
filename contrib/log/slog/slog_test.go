// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	internallog "gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func assertLogEntry(t *testing.T, rawEntry, wantMsg, wantLevel string) {
	t.Helper()

	var data map[string]interface{}
	err := json.Unmarshal([]byte(rawEntry), &data)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	assert.Equal(t, wantMsg, data["msg"])
	assert.Equal(t, wantLevel, data["level"])
	assert.NotEmpty(t, data["time"])
	assert.NotEmpty(t, data[ext.LogKeyTraceID])
	assert.NotEmpty(t, data[ext.LogKeySpanID])
}

func testLogger(t *testing.T, createHandler func(b *bytes.Buffer) slog.Handler) {
	tracer.Start(tracer.WithLogger(internallog.DiscardLogger{}))
	defer tracer.Stop()

	// create the application logger
	var b bytes.Buffer
	h := createHandler(&b)
	logger := slog.New(h)

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	// log a message using the context containing span information
	logger.Log(ctx, slog.LevelInfo, "this is an info log with tracing information")
	logger.Log(ctx, slog.LevelError, "this is an error log with tracing information")

	logs := strings.Split(
		strings.TrimRight(b.String(), "\n"),
		"\n",
	)
	// assert log entries contain trace information
	require.Len(t, logs, 2)
	assertLogEntry(t, logs[0], "this is an info log with tracing information", "INFO")
	assertLogEntry(t, logs[1], "this is an error log with tracing information", "ERROR")
}

func TestNewJSONHandler(t *testing.T) {
	testLogger(t, func(b *bytes.Buffer) slog.Handler {
		return NewJSONHandler(b, nil)
	})
}

func TestWrapHandler(t *testing.T) {
	testLogger(t, func(b *bytes.Buffer) slog.Handler {
		return WrapHandler(slog.NewJSONHandler(b, nil))
	})
}
