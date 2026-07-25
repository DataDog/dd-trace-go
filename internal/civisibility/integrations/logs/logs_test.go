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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

type reentrantLogsTelemetryClient struct {
	*telemetrytest.RecordClient
	once sync.Once
}

type reentrantLogsClientConfigTelemetryClient struct {
	*telemetrytest.RecordClient
	once     sync.Once
	callback func()
}

func (c *reentrantLogsClientConfigTelemetryClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	for _, config := range configs {
		if config.Name == "DD_ENV" {
			c.once.Do(c.callback)
			break
		}
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func (c *reentrantLogsTelemetryClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.once.Do(func() {
		_ = IsEnabled()
	})
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestIsEnabledReportsAfterPublishingCache(t *testing.T) {
	resetGlobalState()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetGlobalState)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	client := &reentrantLogsTelemetryClient{RecordClient: new(telemetrytest.RecordClient)}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = IsEnabled()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logs enablement telemetry reporting deadlocked when the sink reentered IsEnabled")
	}
	require.NotEmpty(t, client.Configuration)
}

// resetGlobalState is a helper that resets the package level variables that keep
// state between invocations. This is required so that each test can start with
// a clean slate and does not depend on execution order.
func resetGlobalState() {
	logsInitMu.Lock()
	defer logsInitMu.Unlock()
	logsMu.Lock()
	defer logsMu.Unlock()
	enabled = nil
	enabledReporter = nil
	enabledReported = false
	logsWriterInstance = nil
	servName = ""
	host = ""
}

func TestIsEnabledConcurrentFirstUseResolvesAndReportsOnce(t *testing.T) {
	resetGlobalState()
	originalPrepare := prepareLogsEnabledConfig
	originalReport := reportLogsConfigEvents
	t.Cleanup(func() {
		prepareLogsEnabledConfig = originalPrepare
		reportLogsConfigEvents = originalReport
		resetGlobalState()
	})

	entered := make(chan struct{})
	release := make(chan struct{})
	var resolves atomic.Int32
	var reports atomic.Int32
	prepareLogsEnabledConfig = func() (bool, []internalconfig.ConfigEvent) {
		resolves.Add(1)
		close(entered)
		<-release
		return true, []internalconfig.ConfigEvent{{Name: "DD_CIVISIBILITY_LOGS_ENABLED"}}
	}
	reportLogsConfigEvents = func([]internalconfig.ConfigEvent) {
		reports.Add(1)
	}

	var wg sync.WaitGroup
	wg.Add(64)
	for range 64 {
		go func() {
			defer wg.Done()
			require.True(t, IsEnabled())
		}()
	}
	<-entered
	close(release)
	wg.Wait()

	require.Equal(t, int32(1), resolves.Load())
	require.Equal(t, int32(1), reports.Load())
}

func TestInitializePublishesWriterBeforeClientConfigCallbacks(t *testing.T) {
	resetGlobalState()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetGlobalState)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	t.Setenv("DD_ENV", "logs-reentrant")
	client := &reentrantLogsClientConfigTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		callback: func() {
			Initialize("reentrant-service")
		},
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		Initialize("outer-service")
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logs writer construction deadlocked when client configuration telemetry reentered Initialize")
	}
	logsMu.Lock()
	writer := logsWriterInstance
	logsMu.Unlock()
	require.NotNil(t, writer)
}

func TestClientConfigCallbacksObservePublishedWriter(t *testing.T) {
	for _, tt := range []struct {
		name     string
		callback func()
		validate func(*testing.T)
	}{
		{
			name: "write",
			callback: func() {
				WriteLog(1, "module", "suite", "test", "message", "")
			},
			validate: func(t *testing.T) {
				logsMu.Lock()
				defer logsMu.Unlock()
				require.NotNil(t, logsWriterInstance)
				require.Equal(t, 1, logsWriterInstance.payload.itemCount())
			},
		},
		{
			name: "stop",
			callback: func() {
				Stop()
			},
			validate: func(t *testing.T) {
				logsMu.Lock()
				defer logsMu.Unlock()
				require.Nil(t, logsWriterInstance)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalState()
			bootstrap.ResetForTesting()
			internalconfig.ResetCIVisibilityForTesting()
			t.Cleanup(resetGlobalState)
			t.Cleanup(bootstrap.ResetForTesting)
			t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
			t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
			t.Setenv("DD_ENV", "logs-reentrant-"+tt.name)
			client := &reentrantLogsClientConfigTelemetryClient{
				RecordClient: new(telemetrytest.RecordClient),
				callback:     tt.callback,
			}
			t.Cleanup(telemetry.MockClient(client))

			done := make(chan struct{})
			go func() {
				defer close(done)
				Initialize("outer-service")
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatalf("logs writer reporter deadlocked when client configuration telemetry reentered %s", tt.name)
			}
			tt.validate(t)
		})
	}
}

func TestWriteLogWaitsForWriterInitialization(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	originalPrepareWriter := prepareLogsWriterFunc
	t.Cleanup(func() { prepareLogsWriterFunc = originalPrepareWriter })
	entered := make(chan struct{})
	release := make(chan struct{})
	prepareLogsWriterFunc = func() (*logsWriter, func()) {
		close(entered)
		<-release
		return &logsWriter{
			client: &MockClient{SendLogsFunc: func(payload io.Reader) error {
				return drainLogsPayload(payload)
			}},
			payload: newLogsPayload(),
			climit:  make(chan struct{}, concurrentConnectionLimit),
		}, func() {}
	}

	initializeDone := make(chan struct{})
	go func() {
		defer close(initializeDone)
		Initialize("write-race")
	}()
	<-entered
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		WriteLog(1, "module", "suite", "test", "message", "")
	}()
	select {
	case <-writeDone:
		t.Fatal("WriteLog returned before writer initialization completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-initializeDone
	<-writeDone

	logsMu.Lock()
	count := logsWriterInstance.payload.itemCount()
	logsMu.Unlock()
	require.Equal(t, 1, count)
}

func TestStopWaitsForWriterInitialization(t *testing.T) {
	resetGlobalState()
	t.Cleanup(resetGlobalState)
	t.Setenv("DD_CIVISIBILITY_LOGS_ENABLED", "true")
	originalPrepareWriter := prepareLogsWriterFunc
	t.Cleanup(func() { prepareLogsWriterFunc = originalPrepareWriter })
	entered := make(chan struct{})
	release := make(chan struct{})
	prepareLogsWriterFunc = func() (*logsWriter, func()) {
		close(entered)
		<-release
		return &logsWriter{
			client: &MockClient{SendLogsFunc: func(payload io.Reader) error {
				return drainLogsPayload(payload)
			}},
			payload: newLogsPayload(),
			climit:  make(chan struct{}, concurrentConnectionLimit),
		}, func() {}
	}

	initializeDone := make(chan struct{})
	go func() {
		defer close(initializeDone)
		Initialize("stop-race")
	}()
	<-entered
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		Stop()
	}()
	select {
	case <-stopDone:
		t.Fatal("Stop returned before writer initialization completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-initializeDone
	<-stopDone

	logsMu.Lock()
	writer := logsWriterInstance
	logsMu.Unlock()
	require.Nil(t, writer)
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
	release := make(chan struct{})
	logsWriterInstance.client = &MockClient{SendLogsFunc: func(payload io.Reader) error {
		<-release
		return drainLogsPayload(payload)
	}}
	logsMu.Unlock()

	var wg sync.WaitGroup
	for i := range 128 {
		wg.Go(func() {
			WriteLog(uint64(i+1), "module", "suite", "test", fmt.Sprintf("message-%d", i), "")
		})
	}
	wg.Go(func() {
		Stop()
	})

	time.Sleep(10 * time.Millisecond)
	close(release)

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent WriteLog and Stop did not complete")
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
