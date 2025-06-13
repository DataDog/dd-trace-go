// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package logs

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsEnabled_CachesValue ensures that once the IsEnabled value is evaluated
// it is cached and further changes to the environment variable do not have any
// effect.
func TestIsEnabled_CachesValue(t *testing.T) {
	resetGlobalState()
	os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED")

	// First call should evaluate the env var (unset => false)
	assert.False(t, IsEnabled())

	// Changing the environment variable afterwards should have no impact
	os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Cleanup(func() { os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED") })

	assert.False(t, IsEnabled(), "IsEnabled should return the cached value and ignore subsequent env changes")
}

// TestLogsPayloadPushAfterRead verifies that attempting to push additional
// entries after the payload has been read results in the expected error.
func TestLogsPayloadPushAfterRead(t *testing.T) {
	p := newLogsPayload()
	assert.NoError(t, p.push(&logEntry{Message: "initial"}))

	// Read the payload to initialize the internal reader
	_, err := io.ReadAll(p)
	assert.NoError(t, err)

	// Now any further push should fail with io.ErrClosedPipe
	err = p.push(&logEntry{Message: "extra"})
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

// TestWriteLog_Serialization checks that WriteLog serialises the expected JSON
// structure into the payload buffer.
func TestWriteLog_Serialization(t *testing.T) {
	resetGlobalState()
	os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Cleanup(func() { os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED") })

	Initialize("serialization-service")
	assert.NotNil(t, logsWriterInstance)

	testID := uint64(12345)
	moduleName := "module"
	suiteName := "suite"
	testName := "test"
	message := "hello world"
	tags := "env:test"

	WriteLog(testID, moduleName, suiteName, testName, message, tags)

	// Extract the payload from the writer and decode it.
	payloadBytes, err := io.ReadAll(logsWriterInstance.payload)
	assert.NoError(t, err)

	var entries logsEntriesPayload
	err = json.Unmarshal(payloadBytes, &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)

	got := entries[0]
	expectedID := strconv.FormatUint(testID, 10)

	assert.Equal(t, "testoptimization", got.DdSource)
	assert.Equal(t, message, got.Message)
	assert.Equal(t, moduleName, got.TestModule)
	assert.Equal(t, suiteName, got.TestSuite)
	assert.Equal(t, testName, got.TestName)
	assert.Equal(t, "serialization-service", got.Service)
	assert.Equal(t, expectedID, got.DdTraceID)
	assert.Equal(t, expectedID, got.DdSpanID)
	assert.Equal(t, tags, got.DdTags)
	// Hostname and Timestamp are environment dependent, so we only check they
	// are non-empty / non-zero.
	assert.NotEmpty(t, got.Hostname)
	assert.NotZero(t, got.Timestamp)
}
