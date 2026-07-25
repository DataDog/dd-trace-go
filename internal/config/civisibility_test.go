// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestCIVisibilityEnabledModePreservesParent(t *testing.T) {
	ResetCIVisibilityForTesting()
	t.Cleanup(ResetCIVisibilityForTesting)
	t.Setenv("DD_CIVISIBILITY_ENABLED", " parent ")

	mode, present := ResolveCIVisibilityEnabledMode()

	require.True(t, present)
	require.Equal(t, CIVisibilityEnabledModeParent, mode)
}

func TestCIVisibilityStableBoolsSkipEmptyDeclarativeValues(t *testing.T) {
	emptyErr := &strconv.NumError{Func: "ParseBool", Num: "", Err: strconv.ErrSyntax}
	for _, key := range []string{"DD_TRACE_DEBUG", "DD_CIVISIBILITY_LOGS_ENABLED"} {
		t.Run(key+"/managed-empty-falls-through", func(t *testing.T) {
			resolved := schema.Resolved[bool]{
				Winner: schema.Winner[bool]{Value: true, Origin: telemetry.OriginEnvVar},
				Attempts: []schema.SourceAttempt{
					{Raw: "false", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig},
					{Raw: "true", Present: true, Valid: true, Origin: telemetry.OriginEnvVar},
					{Raw: "", Present: true, Err: emptyErr, Origin: telemetry.OriginManagedStableConfig},
				},
			}
			filtered, _ := filterEmptyDeclarativeStableAttempts(resolved, nil, key, false, strconv.ParseBool)
			require.True(t, filtered.Winner.Value)
			require.Equal(t, telemetry.OriginEnvVar, filtered.Winner.Origin)
			require.Len(t, filtered.Attempts, 2)
		})

		t.Run(key+"/local-empty-falls-through-to-default", func(t *testing.T) {
			resolved := schema.Resolved[bool]{
				Winner: schema.Winner[bool]{Value: false, Origin: telemetry.OriginDefault, DefaultUsed: true},
				Attempts: []schema.SourceAttempt{
					{Raw: "", Present: true, Err: emptyErr, Origin: telemetry.OriginLocalStableConfig},
				},
			}
			filtered, _ := filterEmptyDeclarativeStableAttempts(resolved, nil, key, false, strconv.ParseBool)
			require.False(t, filtered.Winner.Value)
			require.True(t, filtered.Winner.DefaultUsed)
			require.Empty(t, filtered.Attempts)
		})
	}
}

func TestCIVisibilityTagConfigPreservesExplicitEmptySessionName(t *testing.T) {
	t.Setenv("DD_TEST_SESSION_NAME", "")

	cfg := ResolveCIVisibilityTagConfig()

	require.True(t, cfg.TestSessionNamePresent)
	require.Empty(t, cfg.TestSessionName)
}

func TestCIVisibilitySnapshotCachesFirstUseAndResetResamples(t *testing.T) {
	ResetCIVisibilityForTesting()
	t.Cleanup(ResetCIVisibilityForTesting)
	t.Setenv("DD_CIVISIBILITY_FLAKY_RETRY_COUNT", "7")
	t.Setenv("DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", "101")
	t.Setenv("DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES", "3")

	first := CIVisibilitySnapshot()
	t.Setenv("DD_CIVISIBILITY_FLAKY_RETRY_COUNT", "9")
	t.Setenv("DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", "202")
	t.Setenv("DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES", "4")
	cached := CIVisibilitySnapshot()

	require.Equal(t, 7, first.FlakyRetryCount)
	require.Equal(t, 101, first.TotalFlakyRetryCount)
	require.Equal(t, 3, first.TestManagementAttemptToFixRetries)
	require.Equal(t, first, cached)

	ResetCIVisibilityForTesting()
	resampled := CIVisibilitySnapshot()
	require.Equal(t, 9, resampled.FlakyRetryCount)
	require.Equal(t, 202, resampled.TotalFlakyRetryCount)
	require.Equal(t, 4, resampled.TestManagementAttemptToFixRetries)
}

func TestCIVisibilityRetryParsingPreservesLegacyBoundaries(t *testing.T) {
	tests := []struct {
		name           string
		retries        string
		total          string
		attemptToFix   string
		wantRetries    int
		wantTotal      int
		wantAttemptFix int
	}{
		{
			name:           "zero and negative values remain valid",
			retries:        "0",
			total:          "-2",
			attemptToFix:   "-1",
			wantRetries:    0,
			wantTotal:      -2,
			wantAttemptFix: -1,
		},
		{
			name:           "invalid values use legacy defaults",
			retries:        "not-an-int",
			total:          "not-an-int",
			attemptToFix:   "not-an-int",
			wantRetries:    5,
			wantTotal:      1_000,
			wantAttemptFix: -1,
		},
		{
			name:           "native int maximum remains accepted",
			retries:        strconv.Itoa(maxInt()),
			total:          strconv.Itoa(maxInt()),
			attemptToFix:   strconv.Itoa(maxInt()),
			wantRetries:    maxInt(),
			wantTotal:      maxInt(),
			wantAttemptFix: maxInt(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetCIVisibilityForTesting()
			t.Cleanup(ResetCIVisibilityForTesting)
			t.Setenv("DD_CIVISIBILITY_FLAKY_RETRY_COUNT", tt.retries)
			t.Setenv("DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT", tt.total)
			t.Setenv("DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES", tt.attemptToFix)

			cfg := CIVisibilitySnapshot()

			require.Equal(t, tt.wantRetries, cfg.FlakyRetryCount)
			require.Equal(t, tt.wantTotal, cfg.TotalFlakyRetryCount)
			require.Equal(t, tt.wantAttemptFix, cfg.TestManagementAttemptToFixRetries)
		})
	}
}

func TestCIVisibilityClientConfigResamplesConstructorsAndScrubsSecrets(t *testing.T) {
	const (
		apiKey      = "task9-api-key-secret"
		urlPassword = "task9-url-password"
	)
	t.Setenv("DD_ENV", "first")
	t.Setenv("DD_API_KEY", apiKey)
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", "https://user:"+urlPassword+"@intake.example/path")
	t.Setenv("DD_TAGS", "test.configuration.browser:chrome")

	first, events := resolveCIVisibilityClientConfig()
	require.Equal(t, "first", first.Environment)
	require.Equal(t, apiKey, first.APIKey)
	require.Equal(t, "https://user:"+urlPassword+"@intake.example/path", first.AgentlessURL)
	require.Equal(t, map[string]string{"browser": "chrome"}, first.CustomTestConfigurations)
	require.NotContains(t, fmt.Sprintf("%#v", events), apiKey)
	require.NotContains(t, fmt.Sprintf("%#v", events), urlPassword)

	first.CustomTestConfigurations["browser"] = "mutated"
	t.Setenv("DD_ENV", "second")
	t.Setenv("DD_TAGS", "test.configuration.browser:firefox")
	second := ResolveCIVisibilityClientConfig()
	require.Equal(t, "second", second.Environment)
	require.Equal(t, map[string]string{"browser": "firefox"}, second.CustomTestConfigurations)
}

func TestCIVisibilityConfigEventsUseCanonicalOrdinalsAndPolicies(t *testing.T) {
	t.Setenv("DD_API_KEY", "ordinal-secret")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", "https://user:pass@intake.example")

	_, events := resolveCIVisibilityClientConfig()
	byName := make(map[string][]ConfigEvent)
	for _, event := range events {
		byName[event.Name] = append(byName[event.Name], event)
	}

	for _, name := range []string{"DD_API_KEY", "DD_CIVISIBILITY_AGENTLESS_URL"} {
		require.NotEmpty(t, byName[name])
		var environmentEvent, defaultEvent *ConfigEvent
		for i := range byName[name] {
			event := &byName[name][i]
			require.Equal(t, ReportOncePerGeneration, event.Cadence)
			if event.SourceOrdinal == schema.SourceOrdinalEnvironment && event.Present {
				environmentEvent = event
			}
			if event.SourceOrdinal == schema.SourceOrdinalDefault {
				defaultEvent = event
			}
		}
		require.NotNil(t, environmentEvent)
		require.NotNil(t, defaultEvent)
	}
	require.Equal(t, TelemetryOmit, byName["DD_API_KEY"][0].Policy)
	require.Nil(t, byName["DD_API_KEY"][0].Value)
	require.Equal(t, TelemetrySanitizeURL, byName["DD_CIVISIBILITY_AGENTLESS_URL"][0].Policy)
	require.NotContains(t, fmt.Sprint(byName["DD_CIVISIBILITY_AGENTLESS_URL"]), "pass")
}

func TestCIVisibilityTestOptimizationInvalidPayloadEventIsDiagnosticOnly(t *testing.T) {
	ResetCIVisibilityForTesting()
	t.Cleanup(ResetCIVisibilityForTesting)
	t.Setenv("DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES", " invalid ")

	value, events := PrepareCIVisibilityTestOptimizationConfig()

	require.False(t, value.PayloadsInFiles)
	require.Equal(t, " invalid ", value.PayloadsRaw)
	require.True(t, value.PayloadsPresent)
	var environmentEvent, defaultEvent *ConfigEvent
	for i := range events {
		event := &events[i]
		if event.Name != "DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES" {
			continue
		}
		switch event.SourceOrdinal {
		case schema.SourceOrdinalEnvironment:
			environmentEvent = event
		case schema.SourceOrdinalDefault:
			defaultEvent = event
		}
	}
	require.NotNil(t, environmentEvent)
	require.True(t, environmentEvent.Present)
	require.False(t, environmentEvent.Valid)
	require.Error(t, environmentEvent.Err)
	require.False(t, environmentEvent.ReportValue)
	require.NotNil(t, defaultEvent)
	require.True(t, defaultEvent.ReportValue)
	require.Equal(t, false, defaultEvent.Value)
}

type reentrantCIVisibilityClient struct {
	*telemetrytest.RecordClient
	once sync.Once
}

func (c *reentrantCIVisibilityClient) RegisterAppConfigs(configs ...telemetry.Configuration) {
	c.once.Do(func() {
		_ = CIVisibilitySnapshot()
	})
	c.RecordClient.RegisterAppConfigs(configs...)
}

func TestCIVisibilityFirstUseReportsAfterPublishingSnapshot(t *testing.T) {
	bootstrap.ResetForTesting()
	instrumentationReporter.ResetForTesting()
	ResetCIVisibilityForTesting()
	t.Cleanup(bootstrap.ResetForTesting)
	t.Cleanup(instrumentationReporter.ResetForTesting)
	t.Cleanup(ResetCIVisibilityForTesting)
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED", "false")
	client := &reentrantCIVisibilityClient{RecordClient: new(telemetrytest.RecordClient)}
	t.Cleanup(telemetry.MockClient(client))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = CIVisibilitySnapshot()
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CI Visibility telemetry reporting deadlocked when the sink reentered the snapshot")
	}
	require.NotEmpty(t, client.Configuration)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
