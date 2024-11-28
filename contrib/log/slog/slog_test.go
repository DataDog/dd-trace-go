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
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	internallog "gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func assertLogEntry(t *testing.T, rawEntry, wantMsg, wantLevel string, span tracer.Span, assertExtra func(t *testing.T, entry map[string]interface{})) {
	t.Helper()

	t.Log(rawEntry)

	var entry map[string]interface{}
	err := json.Unmarshal([]byte(rawEntry), &entry)
	require.NoError(t, err)
	require.NotEmpty(t, entry)

	assert.Equal(t, wantMsg, entry["msg"])
	assert.Equal(t, wantLevel, entry["level"])
	assert.NotEmpty(t, entry["time"])

	traceID := strconv.FormatUint(span.Context().TraceID(), 10)
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)
	assert.Equal(t, traceID, entry[ext.LogKeyTraceID], "trace id not found")
	assert.Equal(t, spanID, entry[ext.LogKeySpanID], "span id not found")

	if assertExtra != nil {
		assertExtra(t, entry)
	}
}

func testLogger(t *testing.T, createLogger func(b io.Writer) *slog.Logger, assertExtra func(t *testing.T, entry map[string]interface{})) {
	tracer.Start(tracer.WithLogger(internallog.DiscardLogger{}))
	defer tracer.Stop()

	// create the application logger
	var b bytes.Buffer
	logger := createLogger(&b)

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
	assertLogEntry(t, logs[0], "this is an info log with tracing information", "INFO", span, assertExtra)
	assertLogEntry(t, logs[1], "this is an error log with tracing information", "ERROR", span, assertExtra)
}

func TestNewJSONHandler(t *testing.T) {
	testLogger(
		t,
		func(w io.Writer) *slog.Logger {
			return slog.New(NewJSONHandler(w, nil))
		},
		nil,
	)
}

func TestWrapHandler(t *testing.T) {
	testLogger(
		t,
		func(w io.Writer) *slog.Logger {
			return slog.New(WrapHandler(slog.NewJSONHandler(w, nil)))
		},
		nil,
	)
}

func TestHandlerWithAttrs(t *testing.T) {
	testLogger(
		t,
		func(w io.Writer) *slog.Logger {
			return slog.New(NewJSONHandler(w, nil)).
				With("key1", "val1").
				With(ext.LogKeyTraceID, "trace-id").
				With(ext.LogKeySpanID, "span-id")
		},
		nil,
	)
}

func TestHandlerWithGroup(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		testLogger(
			t,
			func(w io.Writer) *slog.Logger {
				return slog.New(NewJSONHandler(w, nil)).
					WithGroup("some-group").
					With("key1", "val1")
			},
			func(t *testing.T, entry map[string]interface{}) {
				assert.Equal(t, map[string]interface{}{
					"key1": "val1",
				}, entry["some-group"], "group entry not found")
			},
		)
	})

	t.Run("nested groups", func(t *testing.T) {
		testLogger(
			t,
			func(w io.Writer) *slog.Logger {
				return slog.New(NewJSONHandler(w, nil)).
					With("key0", "val0").
					WithGroup("group1").
					With("key1", "val1").
					WithGroup("group1"). // repeat same key again
					With("key1", "val1").
					WithGroup("group2").
					With("key2", "val2").
					With("key3", "val3")
			},
			func(t *testing.T, entry map[string]interface{}) {
				groupKeys := map[string]interface{}{
					"key1": "val1",
					"group1": map[string]interface{}{
						"key1": "val1",
						"group2": map[string]interface{}{
							"key2": "val2",
							"key3": "val3",
						},
					},
				}
				assert.Equal(t, "val0", entry["key0"], "root level key not found")
				assert.Equal(t, groupKeys, entry["group1"], "nested group entries not found")
			},
		)
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

func BenchmarkHandler(b *testing.B) {
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	// create a logger with a bunch of nested groups and fields
	logger := slog.New(NewJSONHandler(io.Discard, nil))
	logger = logger.With("attr1", "val1").WithGroup("group1").With("attr2", "val2").WithGroup("group3").With("attr3", "val3")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.InfoContext(ctx, "some message")
	}
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
