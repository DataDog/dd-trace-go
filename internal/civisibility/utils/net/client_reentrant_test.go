// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package net

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	civisibilityutils "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

type reentrantTelemetryInitializationClient struct {
	*telemetrytest.RecordClient
	once sync.Once
}

func (c *reentrantTelemetryInitializationClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	for _, config := range configs {
		if config.Name == "service" {
			c.once.Do(func() {
				_ = NewClientWithServiceName("reentrant")
			})
			break
		}
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestTelemetryInitializationPublishesBeforeCallbacks(t *testing.T) {
	resetCIVisibilityTelemetryForTesting()
	civisibilityutils.ResetCITags()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetCIVisibilityTelemetryForTesting)
	t.Cleanup(civisibilityutils.ResetCITags)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	client := &reentrantTelemetryInitializationClient{RecordClient: new(telemetrytest.RecordClient)}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = NewClientWithServiceName("outer")
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("telemetry initialization deadlocked when a callback constructed another CI Visibility client")
	}
	require.NotEmpty(t, client.Configuration)
}

func TestConcurrentTelemetryInitializationPublishesExecutorMetadata(t *testing.T) {
	resetCIVisibilityTelemetryForTesting()
	civisibilityutils.ResetCITags()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetCIVisibilityTelemetryForTesting)
	t.Cleanup(civisibilityutils.ResetCITags)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")

	recorder := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(recorder))
	originalGlobalClient := telemetryGlobalClient
	t.Cleanup(func() { telemetryGlobalClient = originalGlobalClient })
	entered := make(chan struct{})
	release := make(chan struct{})
	telemetryGlobalClient = func() telemetry.Client {
		close(entered)
		<-release
		return recorder
	}

	executorDone := make(chan struct{})
	go func() {
		defer close(executorDone)
		_ = NewClientWithServiceName("executor-service")
	}()
	<-entered

	waiterStarted := make(chan struct{})
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		close(waiterStarted)
		_ = NewClientWithServiceName("waiter-service")
	}()
	<-waiterStarted
	close(release)
	<-executorDone
	<-waiterDone

	require.Contains(t, telemetryPreparedConfig, telemetry.Configuration{Name: "service", Value: "executor-service"})
	require.NotContains(t, telemetryPreparedConfig, telemetry.Configuration{Name: "service", Value: "waiter-service"})
	require.Contains(t, recorder.Configuration, telemetry.Configuration{Name: "service", Value: "executor-service"})
	require.NotContains(t, recorder.Configuration, telemetry.Configuration{Name: "service", Value: "waiter-service"})
}

func TestPreparedClientReporterIsIdempotent(t *testing.T) {
	resetCIVisibilityTelemetryForTesting()
	civisibilityutils.ResetCITags()
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	t.Cleanup(resetCIVisibilityTelemetryForTesting)
	t.Cleanup(civisibilityutils.ResetCITags)
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	recorder := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(recorder))

	_, report := PrepareClientWithServiceName("idempotent-service")
	report()
	report()

	var serviceEvents int
	for _, configuration := range recorder.Configuration {
		if configuration.Name == "service" && configuration.Value == "idempotent-service" {
			serviceEvents++
		}
	}
	require.Equal(t, 1, serviceEvents)
}
