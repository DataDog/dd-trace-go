// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package logs

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestLogsSerializationErrorsReportAtFlushBoundary(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	writer := newLogsWriter()
	forceLogsEncodingError(t, writer)
	forceLogsEncodingError(t, writer)

	client.Flush()
	require.Empty(t, rt.LogMessages(), "Error Tracking must wait for a writer boundary")

	writer.flush()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assertLogsEncodingErrorReport(t, string(logs[0].Level), logs[0].Count, logs[0].Message, logs[0].StackTrace)
}

func TestLogsSerializationErrorStopDrainsWithoutSuccessfulEntry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	writer := newLogsWriter()
	forceLogsEncodingError(t, writer)

	client.Flush()
	require.Empty(t, rt.LogMessages(), "Error Tracking must wait for a writer boundary")

	writer.stop()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assertLogsEncodingErrorReport(t, string(logs[0].Level), logs[0].Count, logs[0].Message, logs[0].StackTrace)
}

func TestLogsSerializationErrorKeepsExactLocalLog(t *testing.T) {
	recorder := &localLogRecorder{}
	defer internallog.UseLogger(recorder)()

	writer := newLogsWriter()
	forceLogsEncodingError(t, writer)
	internallog.Flush()

	logs := recorder.logs()
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0], "ERROR: logsWriter: Error encoding JSON: io: read/write on closed pipe")
}

func forceLogsEncodingError(t *testing.T, writer *logsWriter) {
	t.Helper()

	_, err := writer.payload.Read(make([]byte, 1))
	require.NoError(t, err)
	require.False(t, writer.add(&logEntry{}))
}

func assertLogsEncodingErrorReport(t *testing.T, level string, count uint32, message, stackTrace string) {
	t.Helper()

	assert.Equal(t, "ERROR", level)
	assert.EqualValues(t, 1, count, "multiple serialization failures must have one representative report")
	assert.Equal(t, "logsWriter: error encoding JSON: error.error_type=errors.errorString", message)
	assert.Contains(t, stackTrace, "logs_writer.go")
	assert.Contains(t, stackTrace, "reportLogsEncodingError")
}

type localLogRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (r *localLogRecorder) Log(message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, message)
}

func (r *localLogRecorder) logs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messages...)
}

var _ internallog.Logger = (*localLogRecorder)(nil)
