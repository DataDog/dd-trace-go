// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/config/bootstrap"
	"github.com/DataDog/dd-trace-go/v2/internal/config/schema"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace/configbridge"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

func unsetAppSecConfig(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"DD_API_SECURITY_DOWNSTREAM_BODY_ANALYSIS_SAMPLE_RATE",
		"DD_API_SECURITY_ENABLED",
		"DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS",
		"DD_API_SECURITY_PROXY_SAMPLE_RATE",
		"DD_API_SECURITY_REQUEST_SAMPLE_RATE",
		"DD_API_SECURITY_SAMPLE_DELAY",
		"DD_APM_TRACING_ENABLED",
		"DD_APPSEC_ENABLED",
		"DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP",
		"DD_APPSEC_OBFUSCATION_PARAMETER_VALUE_REGEXP",
		"DD_APPSEC_RASP_ENABLED",
		"DD_APPSEC_RULES",
		"DD_APPSEC_TRACE_RATE_LIMIT",
		"DD_APPSEC_WAF_TIMEOUT",
		"DD_TRACE_CLIENT_IP_HEADER",
	} {
		unsetForTest(t, key)
	}
}

func TestAppSecSnapshotPreservesSpecializedParsingAndPresence(t *testing.T) {
	unsetAppSecConfig(t)
	t.Setenv("DD_APPSEC_RULES", "")
	t.Setenv("DD_API_SECURITY_REQUEST_SAMPLE_RATE", "2.5")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "7")
	t.Setenv("DD_APPSEC_TRACE_RATE_LIMIT", "0")
	t.Setenv("DD_APPSEC_OBFUSCATION_PARAMETER_KEY_REGEXP", "")
	t.Setenv("DD_APM_TRACING_ENABLED", "false")
	t.Setenv("DD_TRACE_CLIENT_IP_HEADER", "x-forwarded-client-ip")

	snapshot, _ := ResolveAppSecSnapshot()

	require.True(t, snapshot.RulesPresent)
	require.Nil(t, snapshot.Rules)
	require.NoError(t, snapshot.RulesError)
	require.Equal(t, 1.0, snapshot.APISecuritySampleRate)
	require.Equal(t, 7*time.Microsecond, snapshot.WAFTimeout)
	require.Equal(t, int64(100), snapshot.TraceRateLimit)
	require.Empty(t, snapshot.ObfuscatorKeyRegex)
	require.False(t, snapshot.TracingEnabled)
	require.True(t, snapshot.TracingEnabledPresent)
	require.Equal(t, telemetry.OriginEnvVar, snapshot.TracingEnabledOrigin)
	require.Equal(t, "x-forwarded-client-ip", AppSecClientIPHeader())

	t.Setenv("DD_API_SECURITY_REQUEST_SAMPLE_RATE", "-0.25")
	snapshot, _ = ResolveAppSecSnapshot()
	require.Equal(t, 0.0, snapshot.APISecuritySampleRate)
}

func TestAppSecSnapshotPreservesDefaultsAndInvalidValueWarnings(t *testing.T) {
	unsetAppSecConfig(t)
	logger := new(log.RecordLogger)
	defer log.UseLogger(logger)()
	t.Setenv("DD_API_SECURITY_ENABLED", "invalid")
	t.Setenv("DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS", "invalid")
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "-1s")

	snapshot, _ := ResolveAppSecSnapshot()

	require.True(t, snapshot.APISecurityEnabled)
	require.Equal(t, 1, snapshot.MaxDownstreamRequestBodyAnalysis)
	require.Equal(t, 2*time.Millisecond, snapshot.WAFTimeout)
	logs := strings.Join(logger.Logs(), "\n")
	require.Contains(t, logs, "Non-boolean value for env var DD_API_SECURITY_ENABLED")
	require.Contains(t, logs, "Non-integer value for env var DD_API_SECURITY_MAX_DOWNSTREAM_REQUEST_BODY_ANALYSIS")
}

func TestAppSecRulesAreDefensivelyClonedAndOmittedFromEvents(t *testing.T) {
	unsetAppSecConfig(t)
	path := filepath.Join(t.TempDir(), "private-rules.json")
	const contents = `{"private":"rule-secret"}`
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)

	first, events := ResolveAppSecSnapshot()
	require.Equal(t, contents, string(first.Rules))
	first.Rules[0] = '!'
	first.Origins["DD_APPSEC_RULES"] = telemetry.OriginDefault

	second, _ := ResolveAppSecSnapshot()
	require.Equal(t, contents, string(second.Rules))
	require.Equal(t, telemetry.OriginEnvVar, second.Origins["DD_APPSEC_RULES"])
	for _, event := range events {
		if event.Name != "DD_APPSEC_RULES" {
			continue
		}
		require.Nil(t, event.Value)
		require.NotContains(t, fmt.Sprint(event.Err), path)
		require.NotContains(t, fmt.Sprint(event.Err), "rule-secret")
	}
}

func TestAppSecEnablementPreservesInvalidPresenceAndStableOrigin(t *testing.T) {
	unsetAppSecConfig(t)
	t.Setenv("DD_APPSEC_ENABLED", "invalid")

	enabled, present, origin, err, _ := ResolveAppSecEnablement()
	require.False(t, enabled)
	require.False(t, present)
	require.Equal(t, telemetry.OriginDefault, origin)
	require.Error(t, err)

	t.Setenv("DD_APPSEC_ENABLED", "true")
	enabled, present, origin, err, _ = ResolveAppSecEnablement()
	require.True(t, enabled)
	require.True(t, present)
	require.Equal(t, telemetry.OriginEnvVar, origin)
	require.NoError(t, err)
}

func TestAppSecStableBoolErrorsPreserveLegacyContext(t *testing.T) {
	for _, tc := range []struct {
		key     string
		resolve func() error
	}{
		{
			key: "DD_APPSEC_ENABLED",
			resolve: func() error {
				_, _, _, err, _ := ResolveAppSecEnablement()
				return err
			},
		},
		{
			key:     "DD_APPSEC_SCA_ENABLED",
			resolve: ReportAppSecSCAInitTelemetry,
		},
	} {
		t.Run(tc.key, func(t *testing.T) {
			unsetForTest(t, tc.key)
			t.Setenv(tc.key, "environment-invalid")

			err := tc.resolve()

			require.EqualError(t, err,
				"non-boolean value for "+tc.key+": 'environment-invalid' in env_var configuration, dropping")
		})
	}
}

func TestAppSecStableBoolErrorsUseHighToLowPriorityAndDiscardOnWinner(t *testing.T) {
	attempts := []schema.SourceAttempt{
		{
			Raw: "local-invalid", Present: true, Err: errors.New("invalid"),
			Origin: telemetry.OriginLocalStableConfig,
		},
		{
			Raw: "environment-invalid", Present: true, Err: errors.New("invalid"),
			Origin: telemetry.OriginEnvVar,
		},
		{
			Raw: "managed-invalid", Present: true, Err: errors.New("invalid"),
			Origin: telemetry.OriginManagedStableConfig,
		},
	}

	for _, key := range []string{"DD_APPSEC_ENABLED", "DD_APPSEC_SCA_ENABLED"} {
		resolved := schema.Resolved[bool]{
			Winner: schema.Winner[bool]{
				Value: false, Origin: telemetry.OriginDefault, DefaultUsed: true,
			},
			Attempts: attempts,
		}
		filtered, filteredEvents := filterAppSecStableEmpty(
			resolved,
			appSecStableTestEvents(key, attempts),
			key,
			false,
			strconv.ParseBool,
		)
		require.Equal(t, resolved, filtered)
		require.Equal(t, appSecStableTestEvents(key, attempts), filteredEvents)

		err := appSecStableBoolError(key, filtered.Winner.DefaultUsed, filtered.Attempts)
		require.EqualError(t, err,
			"non-boolean value for "+key+": 'managed-invalid' in fleet_stable_config configuration, dropping\n"+
				"non-boolean value for "+key+": 'environment-invalid' in env_var configuration, dropping\n"+
				"non-boolean value for "+key+": 'local-invalid' in local_stable_config configuration, dropping")
		require.NoError(t, appSecStableBoolError(key, false, attempts))
	}
}

func TestAppSecStableBoolEmptyFileValuesAreAbsent(t *testing.T) {
	for _, key := range []string{"DD_APPSEC_ENABLED", "DD_APPSEC_SCA_ENABLED"} {
		t.Run(key+"/managed-empty-falls-through", func(t *testing.T) {
			emptyErr := &strconv.NumError{Func: "ParseBool", Num: "", Err: strconv.ErrSyntax}
			resolved := schema.Resolved[bool]{
				Winner: schema.Winner[bool]{Value: true, Origin: telemetry.OriginEnvVar},
				Attempts: []schema.SourceAttempt{
					{Raw: "false", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
					{Raw: "true", Present: true, Valid: true, Origin: telemetry.OriginEnvVar},
					{Raw: "", Present: true, Err: emptyErr, Origin: telemetry.OriginManagedStableConfig, ConfigID: "managed"},
				},
			}
			events := appSecStableTestEvents(key, resolved.Attempts)

			got, gotEvents := filterAppSecStableEmpty(resolved, events, key, false, strconv.ParseBool)

			require.Equal(t, schema.Winner[bool]{Value: true, Origin: telemetry.OriginEnvVar}, got.Winner)
			require.NoError(t, appSecStableBoolError(key, got.Winner.DefaultUsed, got.Attempts))
			require.Len(t, got.Attempts, 2)
			require.NotContains(t, got.Attempts, resolved.Attempts[2])
			require.Len(t, gotEvents, 3)
			require.Equal(t, telemetry.OriginEnvVar,
				winnerConfigEvents(gotEvents, key, got.Winner, false)[0].Origin)
		})

		t.Run(key+"/local-empty-falls-through-to-default", func(t *testing.T) {
			emptyErr := &strconv.NumError{Func: "ParseBool", Num: "", Err: strconv.ErrSyntax}
			resolved := schema.Resolved[bool]{
				Winner: schema.Winner[bool]{Value: false, Origin: telemetry.OriginDefault, DefaultUsed: true},
				Attempts: []schema.SourceAttempt{
					{Raw: "", Present: true, Err: emptyErr, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
				},
			}
			events := appSecStableTestEvents(key, resolved.Attempts)

			got, gotEvents := filterAppSecStableEmpty(resolved, events, key, false, strconv.ParseBool)

			require.Equal(t, schema.Winner[bool]{
				Value: false, Origin: telemetry.OriginDefault, DefaultUsed: true,
			}, got.Winner)
			require.Empty(t, got.Attempts)
			require.NoError(t, appSecStableBoolError(key, got.Winner.DefaultUsed, got.Attempts))
			require.Empty(t, winnerConfigEvents(gotEvents, key, got.Winner, false))
			require.Len(t, gotEvents, 1)
			require.Equal(t, telemetry.OriginDefault, gotEvents[0].Origin)
		})
	}
}

func TestAppSecStableEmptyEnvironmentValuesRemainPresent(t *testing.T) {
	key := "DD_APPSEC_ENABLED"
	emptyErr := &strconv.NumError{Func: "ParseBool", Num: "", Err: strconv.ErrSyntax}
	resolved := schema.Resolved[bool]{
		Winner: schema.Winner[bool]{Value: false, Origin: telemetry.OriginDefault, DefaultUsed: true},
		Attempts: []schema.SourceAttempt{
			{Raw: "", Present: true, Err: emptyErr, Origin: telemetry.OriginEnvVar},
		},
	}
	events := appSecStableTestEvents(key, resolved.Attempts)

	got, gotEvents := filterAppSecStableEmpty(resolved, events, key, false, strconv.ParseBool)

	require.Equal(t, resolved, got)
	require.Equal(t, events, gotEvents)
	require.EqualError(t, appSecStableBoolError(key, got.Winner.DefaultUsed, got.Attempts),
		"non-boolean value for DD_APPSEC_ENABLED: '' in env_var configuration, dropping")
}

func TestAppSecAgenticEmptyFileValuesAreAbsent(t *testing.T) {
	const key = "DD_APPSEC_AGENTIC_ONBOARDING"
	t.Run("managed-empty-falls-through-to-environment", func(t *testing.T) {
		resolved := schema.Resolved[string]{
			Winner: schema.Winner[string]{Value: "", Origin: telemetry.OriginManagedStableConfig, ConfigID: "managed"},
			Attempts: []schema.SourceAttempt{
				{Raw: "local", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
				{Raw: "environment", Present: true, Valid: true, Origin: telemetry.OriginEnvVar},
				{Raw: "", Present: true, Valid: true, Origin: telemetry.OriginManagedStableConfig, ConfigID: "managed"},
			},
		}
		events := appSecStableTestEvents(key, resolved.Attempts)

		got, gotEvents := filterAppSecStableEmpty(resolved, events, key, "", func(raw string) (string, error) {
			return raw, nil
		})

		require.Equal(t, schema.Winner[string]{Value: "environment", Origin: telemetry.OriginEnvVar}, got.Winner)
		require.Len(t, got.Attempts, 2)
		require.Len(t, gotEvents, 3)
		require.Equal(t, "environment",
			winnerConfigEvents(gotEvents, key, got.Winner, true)[0].Value)
	})

	t.Run("local-empty-falls-through-to-default", func(t *testing.T) {
		resolved := schema.Resolved[string]{
			Winner: schema.Winner[string]{Value: "", Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
			Attempts: []schema.SourceAttempt{
				{Raw: "", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
			},
		}
		events := appSecStableTestEvents(key, resolved.Attempts)

		got, gotEvents := filterAppSecStableEmpty(resolved, events, key, "", func(raw string) (string, error) {
			return raw, nil
		})

		require.Equal(t, schema.Winner[string]{
			Value: "", Origin: telemetry.OriginDefault, DefaultUsed: true,
		}, got.Winner)
		require.Empty(t, got.Attempts)
		require.Len(t, gotEvents, 1)
		require.Equal(t, telemetry.OriginDefault,
			winnerConfigEvents(gotEvents, key, got.Winner, true)[0].Origin)
	})

	t.Run("environment-empty-remains-the-winner", func(t *testing.T) {
		resolved := schema.Resolved[string]{
			Winner: schema.Winner[string]{Value: "", Origin: telemetry.OriginEnvVar},
			Attempts: []schema.SourceAttempt{
				{Raw: "local", Present: true, Valid: true, Origin: telemetry.OriginLocalStableConfig, ConfigID: "local"},
				{Raw: "", Present: true, Valid: true, Origin: telemetry.OriginEnvVar},
			},
		}
		events := appSecStableTestEvents(key, resolved.Attempts)

		got, gotEvents := filterAppSecStableEmpty(resolved, events, key, "", func(raw string) (string, error) {
			return raw, nil
		})

		require.Equal(t, resolved, got)
		require.Equal(t, events, gotEvents)
		require.Equal(t, telemetry.OriginEnvVar,
			winnerConfigEvents(gotEvents, key, got.Winner, true)[0].Origin)
	})
}

func appSecStableTestEvents(key string, attempts []schema.SourceAttempt) []ConfigEvent {
	events := make([]ConfigEvent, 0, len(attempts)+1)
	for _, attempt := range attempts {
		events = append(events, ConfigEvent{
			Kind: EventConfiguration, Name: key, Value: attempt.Raw,
			Present: attempt.Present, Valid: attempt.Valid, Err: attempt.Err,
			Origin: attempt.Origin, ConfigID: attempt.ConfigID,
		})
	}
	return append(events, ConfigEvent{
		Kind: EventConfiguration, Name: key, Present: true, Valid: true,
		Origin: telemetry.OriginDefault,
	})
}

func TestAPISecuritySamplerConfigPreservesProxyAndDelayParsing(t *testing.T) {
	unsetAppSecConfig(t)
	t.Setenv("DD_API_SECURITY_PROXY_SAMPLE_RATE", "17")
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "4")

	proxyRate, interval := ResolveAPISecuritySamplerConfig(true)
	require.Equal(t, 17, proxyRate)
	require.Equal(t, 30*time.Second, interval)

	proxyRate, interval = ResolveAPISecuritySamplerConfig(false)
	require.Equal(t, 300, proxyRate)
	require.Equal(t, 4*time.Second, interval)
}

func TestStackTraceConfigPreservesEnablementAndDepthParsing(t *testing.T) {
	for _, key := range []string{"DD_APPSEC_STACK_TRACE_ENABLED", "DD_APPSEC_MAX_STACK_TRACE_DEPTH"} {
		unsetForTest(t, key)
	}
	bootstrap.ResetAppSecStackTraceForTesting()
	t.Cleanup(bootstrap.ResetAppSecStackTraceForTesting)
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "true")
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", "64")

	settings := ResolveAppSecStackTraceConfig()
	require.True(t, settings.Enabled)
	require.Equal(t, 64, settings.MaxDepth)
	require.Equal(t, 16, settings.TopFrameDepth)

	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "false")
	bootstrap.ResetAppSecStackTraceForTesting()
	settings = ResolveAppSecStackTraceConfig()
	require.False(t, settings.Enabled)
	require.Equal(t, 32, settings.MaxDepth)
	require.Equal(t, 8, settings.TopFrameDepth)

	// The frozen implementation accepts every value parsed by strconv.Atoi,
	// including zero. Keep that edge behavior during the ownership migration.
	t.Setenv("DD_APPSEC_STACK_TRACE_ENABLED", "true")
	t.Setenv("DD_APPSEC_MAX_STACK_TRACE_DEPTH", "0")
	bootstrap.ResetAppSecStackTraceForTesting()
	settings = ResolveAppSecStackTraceConfig()
	require.True(t, settings.Enabled)
	require.Zero(t, settings.MaxDepth)
	require.Zero(t, settings.TopFrameDepth)
}

func TestStackTraceConfigBridgeIsReentrantAndRaceSafe(t *testing.T) {
	var applied atomic.Int64
	var reentered atomic.Bool
	restoreConsumer := configbridge.SetConsumer(func(settings configbridge.Config) {
		applied.Add(int64(settings.MaxDepth))
		if reentered.CompareAndSwap(false, true) {
			restore := configbridge.SetConsumer(nil)
			restore()
		}
	})
	t.Cleanup(restoreConsumer)
	restoreProvider := configbridge.SetProvider(func() configbridge.Config {
		return configbridge.Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
	})
	t.Cleanup(restoreProvider)
	require.Positive(t, applied.Load())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			restore := configbridge.SetProvider(func() configbridge.Config {
				return configbridge.Config{Enabled: true, MaxDepth: 32, TopFrameDepth: 8}
			})
			restore()
		}()
		go func() {
			defer wg.Done()
			restore := configbridge.SetConsumer(func(configbridge.Config) {})
			restore()
		}()
	}
	wg.Wait()
}

func TestAppSecBlockedTemplatesAreClonedAndPathsAreOmitted(t *testing.T) {
	for _, key := range []string{"DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML", "DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON"} {
		unsetForTest(t, key)
	}
	path := filepath.Join(t.TempDir(), "private-template.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"secret":"value"}`), 0o600))
	t.Setenv("DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON", path)

	snapshot, events := ResolveAppSecBlockedTemplates([]byte("json-default"), []byte("html-default"))
	require.Equal(t, `{"secret":"value"}`, string(snapshot.JSON))
	require.Equal(t, "html-default", string(snapshot.HTML))
	snapshot.JSON[0] = '!'

	again, _ := ResolveAppSecBlockedTemplates([]byte("json-default"), []byte("html-default"))
	require.Equal(t, `{"secret":"value"}`, string(again.JSON))
	for _, event := range events {
		require.Nil(t, event.Value)
		require.NotContains(t, fmt.Sprint(event.Err), path)
		require.NotContains(t, fmt.Sprint(event.Err), "secret")
	}
}

func TestAppSecZeroByteFilesRemainNonNilAcrossSnapshots(t *testing.T) {
	unsetAppSecConfig(t)
	emptyRules := filepath.Join(t.TempDir(), "empty-rules.json")
	emptyJSON := filepath.Join(t.TempDir(), "empty-template.json")
	emptyHTML := filepath.Join(t.TempDir(), "empty-template.html")
	for _, path := range []string{emptyRules, emptyJSON, emptyHTML} {
		require.NoError(t, os.WriteFile(path, []byte{}, 0o600))
	}
	t.Setenv("DD_APPSEC_RULES", emptyRules)
	t.Setenv("DD_APPSEC_HTTP_BLOCKED_TEMPLATE_JSON", emptyJSON)
	t.Setenv("DD_APPSEC_HTTP_BLOCKED_TEMPLATE_HTML", emptyHTML)

	snapshot, _ := ResolveAppSecSnapshot()
	require.NotNil(t, snapshot.Rules)
	require.Empty(t, snapshot.Rules)

	templates, _ := ResolveAppSecBlockedTemplates([]byte{}, []byte{})
	require.NotNil(t, templates.JSON)
	require.Empty(t, templates.JSON)
	require.NotNil(t, templates.HTML)
	require.Empty(t, templates.HTML)
}
