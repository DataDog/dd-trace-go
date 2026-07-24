// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func enableTelemetryForLockTest(t *testing.T, client telemetry.Client) {
	t.Helper()
	bootstrap.ResetForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Cleanup(telemetry.MockClient(client))
}

type getterOnConfigReportClient struct {
	*telemetrytest.RecordClient
	get func()
}

func (c *getterOnConfigReportClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.get()
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestConfigSetterReportsAfterUnlock(t *testing.T) {
	cfg := &Config{overrides: make(map[string]programmaticOverride)}
	client := &getterOnConfigReportClient{
		RecordClient: new(telemetrytest.RecordClient),
		get:          func() { _ = cfg.Debug() },
	}
	enableTelemetryForLockTest(t, client)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cfg.SetDebug(true, telemetry.OriginCode, ProductTracer)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SetDebug deadlocked when configuration telemetry called a getter")
	}
	require.True(t, cfg.Debug())
	require.Len(t, client.Configuration, 1)
}

type callbackMetricHandle struct{}

func (callbackMetricHandle) Submit(float64) {}
func (callbackMetricHandle) Get() float64   { return 0 }

type getterOnConflictClient struct {
	*telemetrytest.RecordClient
	cfg  *Config
	mu   sync.Mutex
	tags []string
}

func (c *getterOnConflictClient) Count(namespace telemetry.Namespace, name string, tags []string) telemetry.MetricHandle {
	if name == "config.product_conflict" {
		c.mu.Lock()
		c.tags = append([]string(nil), tags...)
		c.mu.Unlock()
		_ = c.cfg.Debug()
		return callbackMetricHandle{}
	}
	return c.RecordClient.Count(namespace, name, tags)
}

func (c *getterOnConflictClient) conflictTags() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.tags...)
}

func TestConfigConflictReportsAfterUnlockWithoutValues(t *testing.T) {
	cfg := &Config{
		debug: true,
		overrides: map[string]programmaticOverride{
			"DD_TRACE_DEBUG": {product: ProductTracer, value: true},
		},
	}
	client := &getterOnConflictClient{
		RecordClient: new(telemetrytest.RecordClient),
		cfg:          cfg,
	}
	enableTelemetryForLockTest(t, client)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cfg.SetDebug(false, telemetry.OriginCode, ProductProfiler)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("product conflict telemetry deadlocked when the metric sink called a getter")
	}
	require.True(t, cfg.Debug(), "first-in-wins conflict must reject the second value")
	require.ElementsMatch(t, []string{
		"name:DD_TRACE_DEBUG",
		"first_product:tracer",
		"second_product:profiler",
	}, client.conflictTags())
}

func TestDynamicConfigReportsAfterUnlock(t *testing.T) {
	dynamic := newDynamicConfig("DD_TRACE_ENABLED", true, telemetry.OriginDefault, equal[bool], nil)
	client := &getterOnConfigReportClient{
		RecordClient: new(telemetrytest.RecordClient),
		get:          func() { _ = dynamic.Get() },
	}
	enableTelemetryForLockTest(t, client)

	disabled := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		dynamic.HandleRC(&disabled)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DynamicConfig.HandleRC deadlocked when telemetry called Get")
	}
	require.False(t, dynamic.Get())
}

type reentrantOrderingClient struct {
	*telemetrytest.RecordClient
	onFirst func()
	called  atomic.Bool
}

type blockingNamedConfigClient struct {
	*telemetrytest.RecordClient
	name    string
	entered chan struct{}
	release chan struct{}
	blocked atomic.Bool
}

func (c *blockingNamedConfigClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	if len(configs) > 0 && configs[0].Name == c.name && c.blocked.CompareAndSwap(false, true) {
		close(c.entered)
		<-c.release
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func assertAcceptedBeforeInvertedArrival(
	t *testing.T,
	client *blockingNamedConfigClient,
	olderName string,
	triggerOlder func(),
	waitAccepted func(),
	cfg *Config,
) {
	t.Helper()
	olderDone := make(chan struct{})
	go func() {
		defer close(olderDone)
		triggerOlder()
	}()
	waitAccepted()

	cfg.SetDebug(true, telemetry.OriginCode, ProductTracer)
	<-client.entered
	close(client.release)
	<-olderDone

	require.Len(t, client.Configuration, 2)
	require.Equal(t, "DD_TRACE_DEBUG", client.Configuration[0].Name, "newer transition arrives first")
	require.Equal(t, olderName, client.Configuration[1].Name, "older transition arrives last")
	require.Less(t, client.Configuration[1].SeqID, client.Configuration[0].SeqID,
		"sequence order must follow accepted mutation order")
}

func (c *reentrantOrderingClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	if c.called.CompareAndSwap(false, true) {
		c.onFirst()
	}
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestConfigSetterSequenceFollowsAcceptedMutationWhenSinkArrivalInverts(t *testing.T) {
	cfg := &Config{overrides: make(map[string]programmaticOverride)}
	client := &reentrantOrderingClient{RecordClient: new(telemetrytest.RecordClient)}
	client.onFirst = func() {
		cfg.SetDebug(false, telemetry.OriginCode, ProductTracer)
	}
	enableTelemetryForLockTest(t, client)

	cfg.SetDebug(true, telemetry.OriginCode, ProductTracer)

	require.False(t, cfg.Debug())
	require.Len(t, client.Configuration, 2)
	require.False(t, client.Configuration[0].Value.(bool), "newer value reaches the sink first")
	require.True(t, client.Configuration[1].Value.(bool), "older value reaches the sink last")
	require.Less(t, client.Configuration[1].SeqID, client.Configuration[0].SeqID,
		"sequence order follows accepted mutation order despite inverted arrival")
}

func TestDynamicConfigSequenceFollowsAcceptedMutationWhenSinkArrivalInverts(t *testing.T) {
	dynamic := newDynamicConfig("DD_TRACE_ENABLED", true, telemetry.OriginDefault, equal[bool], nil)
	client := &reentrantOrderingClient{RecordClient: new(telemetrytest.RecordClient)}
	client.onFirst = func() {
		enabled := true
		dynamic.HandleRC(&enabled)
	}
	enableTelemetryForLockTest(t, client)

	disabled := false
	dynamic.HandleRC(&disabled)

	require.True(t, dynamic.Get())
	require.Len(t, client.Configuration, 2)
	require.True(t, client.Configuration[0].Value.(bool), "newer value reaches the sink first")
	require.False(t, client.Configuration[1].Value.(bool), "older value reaches the sink last")
	require.Less(t, client.Configuration[1].SeqID, client.Configuration[0].SeqID)
}

func TestFeatureFlagsReserveSequenceAtAcceptedMutation(t *testing.T) {
	cfg := &Config{overrides: make(map[string]programmaticOverride)}
	features := make([]string, 50_000)
	for i := range features {
		features[i] = fmt.Sprintf("feature-%06d", i)
	}
	last := features[len(features)-1]
	client := &blockingNamedConfigClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_TRACE_FEATURES",
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	enableTelemetryForLockTest(t, client)

	assertAcceptedBeforeInvertedArrival(t, client, "DD_TRACE_FEATURES",
		func() { cfg.SetFeatureFlags(features, telemetry.OriginCode, ProductTracer) },
		func() {
			require.Eventually(t, func() bool { return cfg.HasFeature(last) }, 5*time.Second, time.Millisecond)
		},
		cfg,
	)
}

func TestServiceMappingReservesSequenceAtAcceptedMutation(t *testing.T) {
	cfg := &Config{
		overrides:       make(map[string]programmaticOverride),
		serviceMappings: make(map[string]string, 50_001),
	}
	for i := 0; i < 50_000; i++ {
		cfg.serviceMappings[fmt.Sprintf("from-%06d", i)] = fmt.Sprintf("to-%06d", i)
	}
	client := &blockingNamedConfigClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_SERVICE_MAPPING",
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	enableTelemetryForLockTest(t, client)

	assertAcceptedBeforeInvertedArrival(t, client, "DD_SERVICE_MAPPING",
		func() { cfg.SetServiceMapping("sentinel-from", "sentinel-to", telemetry.OriginCode, ProductTracer) },
		func() {
			require.Eventually(t, func() bool {
				got, ok := cfg.ServiceMapping("sentinel-from")
				return ok && got == "sentinel-to"
			}, 5*time.Second, time.Millisecond)
		},
		cfg,
	)
}

func TestServiceMappingSameKeyArrivalInversionKeepsMutationSequence(t *testing.T) {
	cfg := &Config{serviceMappings: make(map[string]string)}
	client := &blockingNamedConfigClient{
		RecordClient: new(telemetrytest.RecordClient),
		name:         "DD_SERVICE_MAPPING",
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	enableTelemetryForLockTest(t, client)

	olderDone := make(chan struct{})
	go func() {
		defer close(olderDone)
		cfg.SetServiceMapping("first", "one", telemetry.OriginCode, ProductTracer)
	}()
	<-client.entered

	cfg.SetServiceMapping("second", "two", telemetry.OriginCode, ProductTracer)
	close(client.release)
	<-olderDone

	require.Len(t, client.Configuration, 2)
	require.Equal(t, "DD_SERVICE_MAPPING", client.Configuration[0].Name)
	require.Equal(t, "DD_SERVICE_MAPPING", client.Configuration[1].Name)
	require.Less(t, client.Configuration[1].SeqID, client.Configuration[0].SeqID,
		"older accepted transition arrives last but retains the lower sequence")
	require.Equal(t, map[string]string{"first": "one", "second": "two"}, cfg.ServiceMappings())
}
