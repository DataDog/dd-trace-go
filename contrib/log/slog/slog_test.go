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
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/slogtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func assertLogEntry(t *testing.T, rawEntry, wantMsg, wantLevel string, traceID string, spanID string, assertExtra func(t *testing.T, entry map[string]interface{})) {
	t.Helper()

	t.Log(rawEntry)

	var entry map[string]interface{}
	err := json.Unmarshal([]byte(rawEntry), &entry)
	require.NoError(t, err)
	require.NotEmpty(t, entry)

	assert.Equal(t, wantMsg, entry["msg"])
	assert.Equal(t, wantLevel, entry["level"])
	assert.NotEmpty(t, entry["time"])

	assert.Equal(t, traceID, entry[ext.LogKeyTraceID], "trace id not found")
	assert.Equal(t, spanID, entry[ext.LogKeySpanID], "span id not found")

	if assertExtra != nil {
		assertExtra(t, entry)
	}
}

func assertLogEntryNoTrace(t *testing.T, rawEntry, wantMsg, wantLevel string) {
	t.Helper()

	t.Log(rawEntry)

	var entry map[string]interface{}
	err := json.Unmarshal([]byte(rawEntry), &entry)
	require.NoError(t, err)
	require.NotEmpty(t, entry)

	assert.Equal(t, wantMsg, entry["msg"])
	assert.Equal(t, wantLevel, entry["level"])
	assert.NotEmpty(t, entry["time"])

	assert.NotContains(t, entry, ext.LogKeyTraceID)
	assert.NotContains(t, entry, ext.LogKeySpanID)
}

func testLogger(t *testing.T, createLogger func(b io.Writer) *slog.Logger, assertExtra func(t *testing.T, entry map[string]interface{})) {
	tracer.Start(
		tracer.WithTraceEnabled(true),
		tracer.WithLogger(testutils.DiscardLogger()),
	)
	defer tracer.Stop()

	// create the application logger
	var b bytes.Buffer
	logger := createLogger(&b)

	// start a new span
	span, ctx := tracer.StartSpanFromContext(context.Background(), "test")
	defer span.Finish()

	var traceID string
	spanID := strconv.FormatUint(span.Context().SpanID(), 10)
	if os.Getenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED") == "false" {
		// Re-initialize to account for race condition between setting env var in the test and reading it in the contrib
		cfg = newConfig()
		traceID = strconv.FormatUint(span.Context().TraceIDLower(), 10)
	} else {
		traceID = span.Context().TraceID()
	}

	// log a message using the context containing span information
	logger.Log(ctx, slog.LevelInfo, "this is an info log with tracing information")
	logger.Log(ctx, slog.LevelError, "this is an error log with tracing information")

	logs := strings.Split(
		strings.TrimRight(b.String(), "\n"),
		"\n",
	)
	// assert log entries contain trace information
	require.Len(t, logs, 2)
	assertLogEntry(t, logs[0], "this is an info log with tracing information", "INFO", traceID, spanID, assertExtra)
	assertLogEntry(t, logs[1], "this is an error log with tracing information", "ERROR", traceID, spanID, assertExtra)
}

func testLoggerNoTrace(t *testing.T, createLogger func(b io.Writer) *slog.Logger) {
	tracer.Start(
		tracer.WithTraceEnabled(false),
		tracer.WithLogger(testutils.DiscardLogger()),
	)
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
	assertLogEntryNoTrace(t, logs[0], "this is an info log with tracing information", "INFO")
	assertLogEntryNoTrace(t, logs[1], "this is an error log with tracing information", "ERROR")
}

func TestNewJSONHandler(t *testing.T) {
	createLogger := func(w io.Writer) *slog.Logger {
		return slog.New(NewJSONHandler(w, nil))
	}
	testLogger(t, createLogger, nil)
	testLoggerNoTrace(t, createLogger)
}

func TestWrapHandler(t *testing.T) {
	t.Run("testLogger", func(t *testing.T) {
		createLogger := func(w io.Writer) *slog.Logger {
			return slog.New(WrapHandler(slog.NewJSONHandler(w, nil)))
		}
		testLogger(t, createLogger, nil)
		testLoggerNoTrace(t, createLogger)
	})

	t.Run("slogtest", func(t *testing.T) {
		var buf bytes.Buffer
		h := WrapHandler(slog.NewJSONHandler(&buf, nil))
		results := func() []map[string]any {
			var ms []map[string]any
			for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var m map[string]any
				require.NoError(t, json.Unmarshal(line, &m))
				ms = append(ms, m)
			}
			return ms
		}
		require.NoError(t, slogtest.TestHandler(h, results))
	})
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

func Test128BitLoggingDisabled(t *testing.T) {
	os.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	defer os.Unsetenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED")
	t.Run("testLogger", func(t *testing.T) {
		testLogger(
			t,
			func(w io.Writer) *slog.Logger {
				return slog.New(WrapHandler(slog.NewJSONHandler(w, nil)))
			},
			nil,
		)
	})
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
