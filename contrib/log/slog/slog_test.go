// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package slog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

// TestRecordClone is a regression test for https://github.com/DataDog/dd-trace-go/issues/2918.
func TestRecordClone(t *testing.T) {
	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	r := slog.Record{}
	gate := func() {
		// Calling Handle below should not overwrite this value
		r.Add("sentinel-key", "sentinel-value")
	}
	h := handlerGate{gate, WrapHandler(slog.NewJSONHandler(io.Discard, nil))}
	// Up to slog.nAttrsInline (5) attributes are stored in the front array of
	// the record. Make sure to add more records than that to trigger the bug.
	for i := 0; i < 5*10; i++ {
		r.Add("i", i)
	}
	h.Handle(ctx, r)

	var foundSentinel bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "sentinel-key" {
			foundSentinel = true
			return false
		}
		return true
	})
	assert.True(t, foundSentinel)
}

// handlerGate calls a gate function before calling the underlying handler. This
// allows simulating a concurrent modification of the record that happens after
// Handle is called (and the record has been copied), but before the back array
// of the Record is written to.
type handlerGate struct {
	gate func()
	slog.Handler
}

func (h handlerGate) Handle(ctx context.Context, r slog.Record) {
	h.gate()
	h.Handler.Handle(ctx, r)
}
