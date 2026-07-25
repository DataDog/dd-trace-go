// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

type reentrantIntegrationTelemetryClient struct {
	*telemetrytest.RecordClient
	name     string
	once     sync.Once
	callback func()
}

func (c *reentrantIntegrationTelemetryClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	for _, config := range configs {
		if config.Name == c.name {
			c.once.Do(c.callback)
			break
		}
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestBootstrapReportsAfterInitialization(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_TRACE_DEBUG", "true")
	var callbackCalled atomic.Bool
	client := &reentrantIntegrationTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_TRACE_DEBUG",
		callback: func() {
			callbackCalled.Store(true)
			internalCiVisibilityInitialization(func([]tracer.StartOption) {})
		},
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		internalCiVisibilityInitialization(func([]tracer.StartOption) {})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bootstrap telemetry reporting deadlocked when the sink reentered initialization")
	}
	if !callbackCalled.Load() {
		t.Fatal("bootstrap configuration event was not reported")
	}
}

func TestTracerConstructionConfigCallbackCannotReenterInitialization(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_TRACE_AGENT_URL", "http://localhost:8126")
	var callbackCalled atomic.Bool
	client := &reentrantIntegrationTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_TRACE_AGENT_URL",
		callback: func() {
			callbackCalled.Store(true)
			internalCiVisibilityInitialization(func([]tracer.StartOption) {})
		},
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		internalCiVisibilityInitialization(func([]tracer.StartOption) {
			_ = internalconfig.AgentURL()
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tracer-construction configuration telemetry deadlocked while reentering CI initialization")
	}
	if !callbackCalled.Load() {
		t.Fatal("tracer-construction configuration event was not reported")
	}
}

func TestReentrantInternalCIInitializationReturnsWhileExecutorIsInProgress(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		internalCiVisibilityInitialization(func([]tracer.StartOption) {
			close(entered)
			<-release
		})
	}()
	<-entered

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		internalCiVisibilityInitialization(func([]tracer.StartOption) {})
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("concurrent CI initialization did not honor the nonblocking in-progress guard")
	}
	close(release)
	<-firstDone
}

func TestConcurrentInitializeCIVisibilityMockWaitsForInitialization(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityMockModeForTesting)
	originalStartMockTracer := startCIVisibilityMockTracer
	t.Cleanup(func() { startCIVisibilityMockTracer = originalStartMockTracer })
	entered := make(chan struct{})
	release := make(chan struct{})
	startCIVisibilityMockTracer = func() mocktracer.Tracer {
		close(entered)
		<-release
		return originalStartMockTracer()
	}

	firstResult := make(chan mocktracer.Tracer, 1)
	go func() { firstResult <- InitializeCIVisibilityMock() }()
	<-entered

	secondResult := make(chan mocktracer.Tracer, 1)
	go func() { secondResult <- InitializeCIVisibilityMock() }()
	select {
	case result := <-secondResult:
		t.Fatalf("concurrent mock initialization returned before publication: %v", result)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	first := <-firstResult
	second := <-secondResult
	if first == nil || second == nil {
		t.Fatalf("concurrent mock initialization returned nil tracers: first=%v second=%v", first, second)
	}
	if first != second {
		t.Fatal("concurrent mock initialization returned different tracers")
	}
}

func TestSettingsReportsAfterInitialization(t *testing.T) {
	resetCIVisibilityStateForTesting()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", "false")
	prepareCIVisibilityClientWithServiceNameFunc = func(string) (civisibilitynet.Client, func()) {
		return nil, func() {}
	}
	var callbackCalled atomic.Bool
	client := &reentrantIntegrationTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_CIVISIBILITY_GIT_UPLOAD_ENABLED",
		callback: func() {
			callbackCalled.Store(true)
			ensureSettingsInitialization("reentrant")
		},
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		ensureSettingsInitialization("outer")
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("settings telemetry reporting deadlocked when the sink reentered initialization")
	}
	if !callbackCalled.Load() {
		t.Fatal("settings configuration event was not reported")
	}
}

func TestRealClientConfigCallbackCannotReenterSettingsInitialization(t *testing.T) {
	resetCIVisibilityStateForTesting()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", "false")
	t.Setenv("DD_ENV", "settings-reentrant")
	prepareCIVisibilityClientWithServiceNameFunc = func(serviceName string) (civisibilitynet.Client, func()) {
		_, report := civisibilitynet.PrepareClientWithServiceName(serviceName)
		return &mockCIVisibilityClient{
			getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
				return nil, nil
			},
		}, report
	}
	var callbackCalled atomic.Bool
	client := &reentrantIntegrationTelemetryClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_ENV",
		callback: func() {
			callbackCalled.Store(true)
			ensureSettingsInitialization("reentrant")
		},
	}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		ensureSettingsInitialization("outer")
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("real CI client configuration telemetry deadlocked while reentering settings initialization")
	}
	if !callbackCalled.Load() {
		t.Fatal("real CI client configuration event was not reported")
	}
}

func TestConcurrentSettingsInitializationWaitsForExecutor(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	entered := make(chan struct{})
	release := make(chan struct{})
	prepareCIVisibilityClientWithServiceNameFunc = func(string) (civisibilitynet.Client, func()) {
		close(entered)
		<-release
		return nil, func() {}
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		ensureSettingsInitialization("outer")
	}()
	<-entered
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		ensureSettingsInitialization("concurrent")
	}()
	select {
	case <-secondDone:
		t.Fatal("concurrent settings initialization returned before the executor published settings")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-firstDone
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("concurrent settings initialization did not return after the executor completed")
	}
}

func TestSettingsClientReporterRunsAfterBlockingSettingsBootstrap(t *testing.T) {
	resetCIVisibilityStateForTesting()
	t.Cleanup(resetCIVisibilityStateForTesting)
	t.Setenv("DD_CIVISIBILITY_GIT_UPLOAD_ENABLED", "false")
	settingsEntered := make(chan struct{})
	settingsRelease := make(chan struct{})
	reported := make(chan struct{})
	prepareCIVisibilityClientWithServiceNameFunc = func(string) (civisibilitynet.Client, func()) {
		return &mockCIVisibilityClient{
				getSettings: func() (*civisibilitynet.SettingsResponseData, error) {
					close(settingsEntered)
					<-settingsRelease
					return nil, nil
				},
			}, func() {
				close(reported)
			}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ensureSettingsInitialization("outer")
	}()
	<-settingsEntered
	select {
	case <-reported:
		t.Fatal("staged client reporter ran before settings bootstrap published completion")
	case <-time.After(20 * time.Millisecond):
	}
	close(settingsRelease)
	<-done
	select {
	case <-reported:
	default:
		t.Fatal("staged client reporter did not run after settings bootstrap completed")
	}
}
