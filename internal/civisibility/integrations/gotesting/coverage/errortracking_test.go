// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestCoverageSerializationErrorsReportAtFlushBoundary(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	writer := newCoverageWriter()
	writer.mu.Lock()
	writer.recordSerializationErrorLocked(errors.New("first"))
	writer.recordSerializationErrorLocked(errors.New("second"))
	writer.mu.Unlock()

	client.Flush()
	require.Empty(t, rt.LogMessages(), "Error Tracking must wait for a payload boundary")

	// There is no successful payload to upload, but the pending error still
	// needs to be reported at the explicit flush boundary.
	writer.flush()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assertCoverageSerializationError(t, logs[0].Message, logs[0].Level, logs[0].Count, logs[0].StackTrace)

	// Taking and clearing the pending error prevents an empty follow-up flush
	// from sending another report for the same payload window.
	writer.flush()
	client.Flush()
	require.Len(t, rt.LogMessages(), 1)
}

func TestCoverageSerializationErrorsReportOncePerWindow(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	writer := newCoverageWriter()
	for _, err := range []error{errors.New("first"), errors.New("second")} {
		writer.mu.Lock()
		writer.recordSerializationErrorLocked(err)
		writer.mu.Unlock()
	}
	writer.flush()
	client.Flush()

	writer.mu.Lock()
	writer.recordSerializationErrorLocked(errors.New("third"))
	writer.mu.Unlock()
	writer.flush()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 2)
	assertCoverageSerializationError(t, logs[0].Message, logs[0].Level, logs[0].Count, logs[0].StackTrace)
	assertCoverageSerializationError(t, logs[1].Message, logs[1].Level, logs[1].Count, logs[1].StackTrace)
}

func TestCoverageSerializationErrorsReportOnStopWithoutPayload(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	writer := newCoverageWriter()
	writer.mu.Lock()
	writer.recordSerializationErrorLocked(errors.New("stop"))
	writer.mu.Unlock()
	writer.stop()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assertCoverageSerializationError(t, logs[0].Message, logs[0].Level, logs[0].Count, logs[0].StackTrace)
}

func TestCoverageSerializationErrorsPreserveLocalLogKey(t *testing.T) {
	recorder := &internallog.RecordLogger{}
	undo := internallog.UseLogger(recorder)
	defer undo()

	writer := newCoverageWriter()
	writer.mu.Lock()
	writer.recordSerializationErrorLocked(errors.New("first"))
	writer.recordSerializationErrorLocked(errors.New("second"))
	writer.mu.Unlock()
	internallog.Flush()

	logs := recorder.Logs()
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0], "coverageWriter: Error encoding msgpack: first")
	assert.Contains(t, logs[0], "1 additional messages skipped")
}

func assertCoverageSerializationError(t *testing.T, message string, level, count any, stackTrace string) {
	t.Helper()
	assert.Equal(t, "coverageWriter: Error encoding msgpack: error.error_type=errors.errorString", message)
	assert.Equal(t, telemetry.LogError, level)
	assert.EqualValues(t, 1, count)
	assert.NotEmpty(t, stackTrace)
	assert.Contains(t, stackTrace, "coverage_writer.go")
}
