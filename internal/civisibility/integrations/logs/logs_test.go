// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package logs

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetGlobalState is a helper that resets the package level variables that keep
// state between invocations. This is required so that each test can start with
// a clean slate and does not depend on execution order.
func resetGlobalState() {
	logsMu.Lock()
	defer logsMu.Unlock()
	enabledState.Store(logsEnabledUnknown)
	logsWriterInstance = nil
	servName = ""
	host = ""
}

func TestIsEnabled_DefaultsToFalse(t *testing.T) {
	resetGlobalState()
	os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED")

	assert.False(t, IsEnabled(), "IsEnabled should be false when the env var is not set")
}

func TestIsEnabled_EnvVarTrue(t *testing.T) {
	resetGlobalState()
	os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Cleanup(func() { os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED") })

	assert.True(t, IsEnabled(), "IsEnabled should be true when the env var is set to true")
}

func TestIsEnabledCachesImmutableState(t *testing.T) {
	resetGlobalState()
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")

	enabled, cached := cachedEnabled()
	require.False(t, cached)
	require.False(t, enabled)
	require.True(t, IsEnabled())

	enabled, cached = cachedEnabled()
	require.True(t, cached)
	require.True(t, enabled)
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "false")
	require.True(t, IsEnabled(), "log enablement is immutable after its first process-local resolution")
}

func TestInitializeAndStop(t *testing.T) {
	// Make sure feature is enabled
	resetGlobalState()
	os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Cleanup(func() { os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED") })

	Initialize("my-awesome-service")
	assert.NotNil(t, logsWriterInstance, "logsWriterInstance should be set after Initialize")
	assert.Equal(t, "my-awesome-service", servName)
	assert.NotEmpty(t, host, "host should be detected during Initialize")

	Stop()
	assert.Nil(t, logsWriterInstance, "logsWriterInstance should be nil after Stop")
}

func TestWriteLog_WhenDisabled_NoOp(t *testing.T) {
	resetGlobalState()
	os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED")

	// Call WriteLog – it should not panic and should not create a writer.
	WriteLog(123, "module", "suite", "test", "msg", "")
	assert.Nil(t, logsWriterInstance, "logsWriterInstance should remain nil when WriteLog is called while disabled")
}

func TestWriteLog_WritesEntry(t *testing.T) {
	resetGlobalState()
	os.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Cleanup(func() { os.Unsetenv("DD_CIVISIBILITY_LOGS_ENABLED") })

	Initialize("writer-test-service")
	assert.NotNil(t, logsWriterInstance)

	WriteLog(42, "mod", "suite", "test", "hello", "tag:value")

	// Because WriteLog delegates to logsWriterInstance.add which, in turn,
	// stores the entry inside the payload, we can verify that the payload
	// now contains exactly one item.
	assert.Equal(t, 1, logsWriterInstance.payload.itemCount(), "Exactly one log entry should be stored after WriteLog")
}

func TestInitializeWriteLogStopConcurrentRace(t *testing.T) {
	resetGlobalState()
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")

	Initialize("race-test-service")
	logsMu.Lock()
	require.NotNil(t, logsWriterInstance)
	sendEntered := make(chan struct{})
	release := make(chan struct{})
	var sendEnteredOnce sync.Once
	logsWriterInstance.client = &MockClient{SendLogsFunc: func(payload io.Reader) error {
		sendEnteredOnce.Do(func() { close(sendEntered) })
		<-release
		return drainLogsPayload(payload)
	}}
	logsMu.Unlock()

	WriteLog(1, "module", "suite", "test", "seed", "")
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		Stop()
	}()
	<-sendEntered

	var writeWG sync.WaitGroup
	for i := range 128 {
		writeWG.Go(func() {
			WriteLog(uint64(i+1), "module", "suite", "test", fmt.Sprintf("message-%d", i), "")
		})
	}

	writesDone := make(chan struct{})
	go func() {
		defer close(writesDone)
		writeWG.Wait()
	}()
	select {
	case <-writesDone:
	case <-time.After(time.Second):
		t.Fatal("WriteLog calls did not complete while Stop was flushing")
	}

	close(release)
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop did not complete after the upload was released")
	}
}

func TestLogsPayloadResetAndRead(t *testing.T) {
	p := newLogsPayload()
	for i := range 5 {
		p.push(&logEntry{Message: "msg" + strconv.Itoa(i)})
	}

	// Read entire payload once.
	first, err := io.ReadAll(p)
	assert.NoError(t, err)
	assert.NotEmpty(t, first)

	// Reset and read again – bytes should match the first read.
	p.reset()
	second, err := io.ReadAll(p)
	assert.NoError(t, err)
	assert.Equal(t, first, second, "Payload contents should be identical after reset")
}

func TestLogsPayloadClear(t *testing.T) {
	p := newLogsPayload()
	p.push(&logEntry{Message: "msg"})

	assert.Greater(t, p.size(), 0, "Size should be > 0 after pushing an entry")

	p.clear()

	assert.Equal(t, 0, p.itemCount())
	assert.LessOrEqual(t, p.size(), 2, "Size should be minimal after clear")
}
