// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

// withLogLevel sets the global log level for the duration of the test and
// captures every message logged meanwhile.
func withLogLevel(t *testing.T, lvl log.Level) *capturingLogger {
	t.Helper()
	prev := log.GetLevel()
	log.SetLevel(lvl)
	logger, undo := newCapturingLogger()
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})
	return logger
}

func (l *capturingLogger) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.messages...)
}

func (l *capturingLogger) assertContains(t *testing.T, substr string) {
	t.Helper()
	assert.Positive(t, l.countContaining(substr), "expected a log containing %q, got:\n%s", substr, strings.Join(l.all(), "\n"))
}

func TestDebugLoggingHook_Finally(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	hookCtx := of.NewHookContext(
		"my-flag",
		of.Boolean,
		false,
		of.NewClientMetadata("test"),
		of.Metadata{Name: datadogProviderName},
		of.NewEvaluationContext("secret-user-id", map[string]any{"email": "secret@example.com"}),
	)
	details := of.InterfaceEvaluationDetails{
		Value: true,
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "my-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant:      "on",
				Reason:       of.TargetingMatchReason,
				FlagMetadata: of.FlagMetadata{metadataAllocationKey: "alloc-1"},
			},
		},
	}

	newDebugLoggingHook().Finally(context.Background(), hookCtx, details, of.HookHints{})

	require.Len(t, logger.all(), 1)
	msg := logger.all()[0]
	assert.Contains(t, msg, `openfeature: evaluated "my-flag": type=bool`)
	assert.Contains(t, msg, "value=true")
	assert.Contains(t, msg, `variant="on"`)
	assert.Contains(t, msg, "reason=TARGETING_MATCH")
	assert.Contains(t, msg, `allocation="alloc-1"`)
	assert.NotContains(t, msg, "secret-user-id", "the targeting key must never be logged")
	assert.NotContains(t, msg, "secret@example.com", "context attributes must never be logged")
}

func TestDebugLoggingHook_FinallyLogsErrors(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	hookCtx := of.NewHookContext("missing", of.String, "dflt", of.NewClientMetadata("test"), of.Metadata{}, of.EvaluationContext{})
	details := of.InterfaceEvaluationDetails{
		Value: "dflt",
		EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "missing",
			FlagType: of.String,
			ResolutionDetail: of.ResolutionDetail{
				Reason:       of.ErrorReason,
				ErrorCode:    of.FlagNotFoundCode,
				ErrorMessage: "flag not found",
			},
		},
	}

	newDebugLoggingHook().Finally(context.Background(), hookCtx, details, of.HookHints{})

	logger.assertContains(t, `evaluated "missing": type=string, value="dflt", variant="", reason=ERROR, allocation="", error_code=FLAG_NOT_FOUND, error_message="flag not found"`)
}

func TestDebugLoggingHook_SilentWhenDebugDisabled(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)

	hookCtx := of.NewHookContext("my-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{}, of.EvaluationContext{})
	newDebugLoggingHook().Finally(context.Background(), hookCtx, of.InterfaceEvaluationDetails{Value: true}, of.HookHints{})

	assert.Empty(t, logger.all())
}

func TestFormatDebugValue(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"bool", false, "false"},
		{"string", "a \"b\"", `"a \"b\""`},
		{"int64", int64(-42), "-42"},
		{"int", 7, "7"},
		{"float", 1.5, "1.5"},
		{"object", map[string]any{"k": []any{1, "v"}}, `{"k":[1,"v"]}`},
		{"unserializable", make(chan int), "<unserializable>"},
		{"truncated", strings.Repeat("x", 300), `"` + strings.Repeat("x", maxDebugValueLen-1) + "...(truncated)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, formatDebugValue(tc.value))
		})
	}
}

func hasDebugLoggingHook(hooks []of.Hook) bool {
	for _, h := range hooks {
		if _, ok := h.(*debugLoggingHook); ok {
			return true
		}
	}
	return false
}

func TestDebugLoggingHook_Registration(t *testing.T) {
	t.Run("registered when debug logging is enabled", func(t *testing.T) {
		withLogLevel(t, log.LevelDebug)
		assert.True(t, hasDebugLoggingHook(newDatadogProvider(ProviderConfig{}).Hooks()))
	})

	t.Run("not registered when debug logging is disabled", func(t *testing.T) {
		withLogLevel(t, log.LevelWarn)
		assert.False(t, hasDebugLoggingHook(newDatadogProvider(ProviderConfig{}).Hooks()))
	})
}

// TestDebugLoggingHook_EndToEnd checks that the OpenFeature SDK actually runs
// the hook for a real evaluation, not just that it is registered.
func TestDebugLoggingHook_EndToEnd(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())

	const domain = "debug-logging-hook-e2e"
	require.NoError(t, of.SetNamedProviderAndWait(domain, p))
	t.Cleanup(func() { _ = of.SetNamedProviderAndWait(domain, of.NoopProvider{}) })

	client := of.NewClient(domain)
	value, err := client.BooleanValue(context.Background(), "bool-flag", false, of.NewEvaluationContext("user-1", map[string]any{"country": "US"}))
	require.NoError(t, err)
	require.True(t, value)

	_, err = client.BooleanValue(context.Background(), "does-not-exist", false, of.NewEvaluationContext("user-1", nil))
	require.Error(t, err)

	logger.assertContains(t, `evaluated "bool-flag": type=bool, value=true, variant="on", reason=TARGETING_MATCH, allocation="allocation1"`)
	logger.assertContains(t, `evaluated "does-not-exist": type=bool, value=false, variant="", reason=ERROR, allocation="", error_code=FLAG_NOT_FOUND`)
}

func newDebugAgentlessSource(t *testing.T, baseURL string) *agentlessSource {
	t.Helper()
	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: baseURL,
		Env:              "staging",
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}, func(*universalFlagsConfiguration) {})
	require.NoError(t, err)
	src.retryDelay = func(int) time.Duration { return 0 }
	return src
}

func TestAgentlessSource_DebugLogs(t *testing.T) {
	t.Run("applied configuration", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("valid")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		logger.assertContains(t, "openfeature: agentless configuration poll attempt 1/3")
		logger.assertContains(t, "openfeature: agentless configuration poll attempt 1/3 returned HTTP 200")
		logger.assertContains(t, "openfeature: agentless configuration applied successfully (0 flag(s), env=staging)")
	})

	t.Run("applied configuration without DD_ENV", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("valid")

		newAgentlessSourceOnly(t, backend, time.Hour, func(*universalFlagsConfiguration) {}).poll()

		logger.assertContains(t, "openfeature: agentless configuration applied successfully (0 flag(s), env=unset)")
	})

	t.Run("unchanged configuration", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("not_modified")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		logger.assertContains(t, "openfeature: agentless configuration poll attempt 1/3 returned HTTP 304 (unchanged)")
		assert.Zero(t, logger.countContaining("applied successfully"))
	})

	t.Run("retryable status exhausts attempts", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("server_error")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		for _, attempt := range []string{"1/3", "2/3", "3/3"} {
			logger.assertContains(t, "openfeature: agentless configuration poll attempt "+attempt+" returned HTTP 500")
		}
		// Every attempt is logged at debug, while the warning stays deduplicated.
		assert.Equal(t, 3, logger.countContaining("returned HTTP 500"))
		assert.Equal(t, 1, logger.countContaining("received retryable status 500"))
		assert.Equal(t, 2, logger.countContaining("openfeature: agentless configuration poll retrying in"))
		logger.assertContains(t, "openfeature: agentless configuration poll failed after 3 attempts; keeping the last known configuration until the next poll")
	})

	t.Run("authentication failure", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("unauthorized")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		logger.assertContains(t, "returned HTTP 401")
		assert.Zero(t, logger.countContaining("retrying in"), "an authentication failure is not retried")
	})

	t.Run("transport failure", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close() // nothing is listening anymore: connection refused

		newDebugAgentlessSource(t, srv.URL).poll()

		for _, attempt := range []string{"1/3", "2/3", "3/3"} {
			logger.assertContains(t, "openfeature: agentless configuration poll attempt "+attempt+" failed: ")
		}
		assert.Equal(t, 1, logger.countContaining("openfeature: agentless: request failed:"), "the warning stays deduplicated")
	})

	t.Run("silent when debug logging is disabled", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		backend := newFakeUFCBackend(t)
		backend.setResponses("valid")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		assert.Empty(t, logger.all())
	})
}

// TestAgentlessDebugLogs_NeverContainEndpointSecrets extends
// TestAgentlessSource_NoLogContainsTheEndpoint to the debug level, where the
// new per-attempt and startup lines are emitted.
func TestAgentlessDebugLogs_NeverContainEndpointSecrets(t *testing.T) {
	const secret = "s3cr3t-token"
	logger := withLogLevel(t, log.LevelDebug)

	for _, responses := range [][]string{
		{"valid"},
		{"unauthorized"},
		{"malformed"},
		{"server_error"},
	} {
		backend := newFakeUFCBackend(t)
		backend.setResponses(responses...)
		u, err := url.Parse(backend.server.URL)
		require.NoError(t, err)
		u.User = url.UserPassword("user", secret)
		u.Path = "/" + secret
		u.RawQuery = "sig=" + secret

		settings := internalffe.Settings{
			AgentlessBaseURL: u.String(),
			PollInterval:     time.Hour,
			RequestTimeout:   2 * time.Second,
		}
		p, err := startWithAgentless(ProviderConfig{}, settings)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return logger.countContaining("returned HTTP") > 0
		}, 2*time.Second, time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		require.NoError(t, p.ShutdownWithContext(ctx))
		cancel()

		logger.assertContains(t, "openfeature: starting agentless configuration source at http://"+u.Host+" (poll interval 1h0m0s, request timeout 2s)")
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	newDebugAgentlessSource(t, "http://user:"+secret+"@"+srv.Listener.Addr().String()+"/"+secret+"?sig="+secret).poll()
	logger.assertContains(t, "agentless configuration poll attempt 1/3 failed: ")

	assert.Zero(t, logger.countContaining(secret), "logs:\n%s", strings.Join(logger.all(), "\n"))
}

func TestRedactedOrigin(t *testing.T) {
	assert.Equal(t, "https://example.com:8443", redactedOrigin("https://user:pw@example.com:8443/token/path?sig=abc#frag"))
	assert.Equal(t, "<invalid URL>", redactedOrigin("not a url"))
	assert.Equal(t, "<invalid URL>", redactedOrigin("://bad"))
}

func TestInitWithContext_DebugLogs(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newDatadogProvider(ProviderConfig{})
		p.updateConfiguration(createTestConfig())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, p.InitWithContext(ctx, of.EvaluationContext{}))

		logger.assertContains(t, "openfeature: waiting for provider initialization... (timeout ")
		logger.assertContains(t, "openfeature: provider initialized successfully in ")
	})

	t.Run("timeout", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newDatadogProvider(ProviderConfig{})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		require.Error(t, p.InitWithContext(ctx, of.EvaluationContext{}))

		logger.assertContains(t, "openfeature: provider failed to initialize: ")
		logger.assertContains(t, "initialization timed out before configuration arrived")
		assert.Zero(t, logger.countContaining("initialized successfully"))
	})

	t.Run("delivery error", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p, err := startWithAgentless(ProviderConfig{}, internalffe.Settings{PollInterval: time.Hour, RequestTimeout: time.Second})
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.Error(t, p.InitWithContext(ctx, of.EvaluationContext{}))
		log.Flush() // log.Error is aggregated until flushed

		// The root cause is logged as soon as it is discovered, then summarized by Init.
		logger.assertContains(t, "openfeature: failed to start agentless configuration source: agentless: no API key configured for the managed endpoint")
		logger.assertContains(t, "openfeature: provider failed to initialize: ")
		logger.assertContains(t, "no API key configured")
	})
}

func TestNewDatadogProvider_DebugLogs(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })

	t.Run("DD_TRACE_DEBUG enables debug logging without tracer.Start", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_TRACE_DEBUG", "true")
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

		_, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)

		assert.True(t, log.DebugEnabled())
		logger.assertContains(t, "openfeature: feature flagging is disabled; flag evaluations will return their default values")
	})

	t.Run("without DD_TRACE_DEBUG the level is left alone", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

		_, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)

		assert.False(t, log.DebugEnabled())
		assert.Empty(t, logger.all())
	})

	t.Run("logs the selected remote_config source", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		// Attach to a simulated tracer subscription so no Agent is needed.
		internalffe.ResetForTest()
		internalffe.SetSubscribedForTest(true)
		t.Cleanup(internalffe.ResetForTest)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")

		p, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.(*DatadogProvider).ShutdownWithContext(ctx)
		})

		logger.assertContains(t, "openfeature: configuration source is remote_config")
		logger.assertContains(t, "openfeature: starting remote_config configuration source (Agent Remote Configuration)")
	})

	t.Run("logs the selected agentless source", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "agentless")
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_BASE_URL", backend.server.URL)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_POLL_INTERVAL_SECONDS", "3600")

		p, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.(*DatadogProvider).ShutdownWithContext(ctx)
		})

		logger.assertContains(t, "openfeature: configuration source is agentless")
		logger.assertContains(t, "openfeature: starting agentless configuration source at "+backend.server.URL)
	})
}

func TestFlagEvalMetrics_DebugLogsWhenDisabled(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	m, err := newFlagEvalMetrics()
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.shutdown(context.Background()) })

	logger.assertContains(t, "openfeature: evaluation metrics disabled; set DD_METRICS_OTEL_ENABLED=true to enable")
}

func TestFlagEvalCounts_DebugLogsWhenDisabled(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	t.Setenv(flagEvalCountsEnabledEnvVar, "false")

	p := newDatadogProvider(ProviderConfig{})

	assert.Nil(t, p.flagEvalLoggingHook)
	logger.assertContains(t, "openfeature: evaluation counts disabled; set DD_FLAGGING_EVALUATION_COUNTS_ENABLED=true to enable")
}

func TestRemoteConfig_DebugLogs(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	p := newDatadogProvider(ProviderConfig{})
	cfg := createTestConfig()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)

	const path = "datadog/2/FFE_FLAGS/config/config"
	processConfigUpdate(p, path, data)
	processConfigUpdate(p, path, nil)

	logger.assertContains(t, `openfeature: remote_config configuration "`+path+`" received`)
	logger.assertContains(t, `openfeature: remote_config configuration "`+path+`" applied successfully (`+strconv.Itoa(len(cfg.Flags))+` flag(s))`)
	logger.assertContains(t, `openfeature: remote_config configuration "`+path+`" removed`)
}
