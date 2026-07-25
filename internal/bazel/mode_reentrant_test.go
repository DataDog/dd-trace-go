// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package bazel_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/bazel"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

type reentrantModeTelemetryClient struct {
	*telemetrytest.RecordClient
	once sync.Once
}

func (c *reentrantModeTelemetryClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.once.Do(func() {
		_ = bazel.CurrentMode()
	})
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestBazelCurrentModeReportsAfterPublishingCache(t *testing.T) {
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	bazel.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Cleanup(bazel.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	client := &reentrantModeTelemetryClient{RecordClient: new(telemetrytest.RecordClient)}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		value, events := internalconfig.PrepareCIVisibilityTestOptimizationConfig()
		_, report := bazel.PrepareCurrentModeWithConfig(
			value.ManifestFile,
			value.PayloadsInFiles,
			value.PayloadsRaw,
			value.PayloadsPresent,
			internalconfig.CIVisibilityConfigEventsReporter(events),
		)
		report()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Bazel mode telemetry reporting deadlocked when the sink reentered CurrentMode")
	}
	if len(client.Configuration) == 0 {
		t.Fatal("expected Bazel mode configuration telemetry")
	}
}

func TestStandaloneModeCanAdoptRegistryReporterLater(t *testing.T) {
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	bazel.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Cleanup(bazel.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	client := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(client))

	require.True(t, bazel.CurrentMode().PayloadFilesEnabled)
	value, events := internalconfig.PrepareCIVisibilityTestOptimizationConfig()
	_, report := bazel.PrepareCurrentModeWithConfig(
		value.ManifestFile,
		value.PayloadsInFiles,
		value.PayloadsRaw,
		value.PayloadsPresent,
		internalconfig.CIVisibilityConfigEventsReporter(events),
	)
	report()

	require.NotEmpty(t, client.Configuration)
}

func TestConcurrentBootstrapClaimCannotLoseModeReporter(t *testing.T) {
	bootstrap.ResetForTesting()
	internalconfig.ResetCIVisibilityForTesting()
	bazel.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(internalconfig.ResetCIVisibilityForTesting)
	t.Cleanup(bazel.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv(bazel.PayloadsInFilesEnv, "true")
	client := new(telemetrytest.RecordClient)
	t.Cleanup(telemetry.MockClient(client))

	type prepared struct {
		value  internalconfig.CIVisibilityTestOptimizationConfig
		events []internalconfig.ConfigEvent
	}
	results := make(chan prepared, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			value, events := internalconfig.PrepareCIVisibilityTestOptimizationConfig()
			results <- prepared{value: value, events: events}
		}()
	}
	wg.Wait()
	close(results)

	var owner, nonowner prepared
	for result := range results {
		if len(result.events) == 0 {
			nonowner = result
		} else {
			owner = result
		}
	}
	require.NotEmpty(t, owner.events)
	require.Empty(t, nonowner.events)

	_, reportNonowner := bazel.PrepareCurrentModeWithConfig(
		nonowner.value.ManifestFile,
		nonowner.value.PayloadsInFiles,
		nonowner.value.PayloadsRaw,
		nonowner.value.PayloadsPresent,
		internalconfig.CIVisibilityConfigEventsReporter(nonowner.events),
	)
	reportNonowner()
	_, reportOwner := bazel.PrepareCurrentModeWithConfig(
		owner.value.ManifestFile,
		owner.value.PayloadsInFiles,
		owner.value.PayloadsRaw,
		owner.value.PayloadsPresent,
		internalconfig.CIVisibilityConfigEventsReporter(owner.events),
	)
	reportOwner()

	require.NotEmpty(t, client.Configuration)
}
