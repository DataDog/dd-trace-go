// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/open-feature/go-sdk/openfeature/isolated"
	"github.com/open-feature/go-sdk/openfeature/memprovider"
	"github.com/open-feature/go-sdk/openfeature/multi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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

func TestLogResolution(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	logResolution(nil, "my-flag", of.Boolean, false, of.FlattenedContext{
		of.TargetingKey: "user-1",
		"country":       "US",
		"tier":          int64(3),
	}, of.BoolResolutionDetail{
		Value: true,
		ProviderResolutionDetail: of.ProviderResolutionDetail{
			Variant:      "on",
			Reason:       of.TargetingMatchReason,
			FlagMetadata: of.FlagMetadata{metadataAllocationKey: "alloc-1"},
		},
	})

	require.Len(t, logger.all(), 1)
	logger.assertContains(t, `openfeature: provider resolved "my-flag": type=bool, value=true, default=false, variant="on", reason=TARGETING_MATCH, allocation="alloc-1", serial_id=, error_code=, error_message="", targeting_key="user-1", attributes={"country":"US","tier":3}`)
}

func TestLogResolution_Errors(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	logResolution(nil, "missing", of.String, "dflt", nil, of.StringResolutionDetail{
		Value: "dflt",
		ProviderResolutionDetail: of.ProviderResolutionDetail{
			ResolutionError: of.NewFlagNotFoundResolutionError("flag not found"),
			Reason:          of.ErrorReason,
		},
	})

	// A context without a targeting key logs null, distinct from an empty key.
	logger.assertContains(t, `provider resolved "missing": type=string, value="dflt", default="dflt", variant="", reason=ERROR, allocation="", serial_id=, error_code=FLAG_NOT_FOUND, error_message="flag not found", targeting_key=null, attributes={}`)
}

func TestLogResolution_SilentWhenDebugDisabled(t *testing.T) {
	logger := withLogLevel(t, log.LevelWarn)

	detail := of.StringResolutionDetail{Value: "on", ProviderResolutionDetail: of.ProviderResolutionDetail{Reason: of.StaticReason}}
	flatCtx := of.FlattenedContext{of.TargetingKey: "user-1", "country": "US"}
	warned := new(atomic.Bool)
	allocs := testing.AllocsPerRun(100, func() { logResolution(warned, "my-flag", of.String, "off", flatCtx, detail) })

	assert.Zero(t, allocs, "the value must not be boxed while debug logging is off")
	assert.Empty(t, logger.all())
	assert.False(t, warned.Load())
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
		{"int32", int32(-3), "-3"},
		{"float", 1.5, "1.5"},
		{"float32", float32(0.5), "0.5"},
		{"object", map[string]any{"k": []any{1, "v"}}, `{"k":[1,"v"]}`},
		{"large object", map[string]any{"k": strings.Repeat("x", 300)}, `{"k":"` + strings.Repeat("x", 300) + `"}`},
		{"panicking marshaler", panickingValue{}, "<unserializable>"},
		{"panicking marshaler in an array", []any{panickingValue{}}, "<unserializable>"},
		{"typed nil", (*panickingValue)(nil), "null"},
		{"named type", namedString("x"), `"x"`},
		{"unsupported type", make(chan int), "<unserializable>"},
		{"large string", strings.Repeat("x", 300), `"` + strings.Repeat("x", 300) + `"`},
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

	t.Run("registered when debug logging is disabled", func(t *testing.T) {
		withLogLevel(t, log.LevelWarn)
		assert.True(t, hasDebugLoggingHook(newDatadogProvider(ProviderConfig{}).Hooks()))
	})
}

func registerDebugLoggingProvider(t *testing.T, domain string, p *DatadogProvider) {
	t.Helper()
	t.Cleanup(func() {
		// Replacing a provider only waits for the replacement's initialization.
		// Finish the old provider's teardown before restoring the global logger.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, p.ShutdownWithContext(ctx))
		assert.NoError(t, of.SetNamedProviderAndWait(domain, of.NoopProvider{}))
	})
	require.NoError(t, of.SetNamedProviderAndWait(domain, p))
}

// TestDebugLoggingHook_EndToEnd checks that the OpenFeature SDK actually runs
// the hook for a real evaluation, not just that it is registered.
func TestDebugLoggingHook_EndToEnd(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())

	const domain = "debug-logging-hook-e2e"
	registerDebugLoggingProvider(t, domain, p)

	client := of.NewClient(domain)
	value, err := client.BooleanValue(context.Background(), "bool-flag", false, of.NewEvaluationContext("user-1", map[string]any{"country": "US"}))
	require.NoError(t, err)
	require.True(t, value)

	_, err = client.BooleanValue(context.Background(), "does-not-exist", false, of.NewEvaluationContext("user-1", nil))
	require.Error(t, err)

	logger.assertContains(t, `provider resolved "bool-flag": type=bool, value=true, default=false, variant="on", reason=TARGETING_MATCH, allocation="allocation1"`)
	logger.assertContains(t, `targeting_key="user-1", attributes={"country":"US"}`)
	logger.assertContains(t, `provider resolved "does-not-exist": type=bool, value=false, default=false, variant="", reason=ERROR, allocation="", serial_id=, error_code=FLAG_NOT_FOUND`)
	logger.assertContains(t, `evaluation of "does-not-exist" failed: error code: FLAG_NOT_FOUND: flag not found: "does-not-exist" (default value false, targeting_key="user-1", attributes={})`)
}

// failingAfterHook is an application hook whose After stage fails after the
// provider resolved the flag.
type failingAfterHook struct {
	of.UnimplementedHook
}

func (failingAfterHook) After(context.Context, of.HookContext, of.InterfaceEvaluationDetails, of.HookHints) error {
	return errors.New("application after hook failed")
}

// assertNoReturnedValueClaims fails if a log line about flagKey claims which
// value the application received, which the hooks cannot know, or reports a
// flag value other than in the provider's resolution line.
func assertNoReturnedValueClaims(t *testing.T, logger *capturingLogger, flagKey string) {
	t.Helper()
	quoted := strconv.Quote(flagKey)
	for _, msg := range logger.all() {
		if !strings.Contains(msg, quoted) {
			continue
		}
		assert.NotContains(t, msg, "return", "no line may claim which value the application received")
		if strings.Contains(msg, " value=") {
			assert.Contains(t, msg, "provider resolved "+quoted, "only the provider's resolution may report a flag value")
		}
	}
}

// TestDebugLogging_AfterHookFailure covers an application After hook failing
// after the provider resolved the flag. The SDK passes Finally hooks the
// resolution as if the evaluation had succeeded, and what the application
// receives depends on the call, so the logs must report the resolution and
// the failure without claiming either was returned.
func TestDebugLogging_AfterHookFailure(t *testing.T) {
	evalCtx := of.NewEvaluationContext("user-1", map[string]any{"country": "US"})
	for _, tc := range []struct {
		name     string
		flagType string
		evaluate func(*of.Client) (any, error)
		want     any
	}{
		{
			name:     "BooleanValue returns the default",
			flagType: "bool",
			evaluate: func(c *of.Client) (any, error) {
				return c.BooleanValue(context.Background(), "bool-flag", false, evalCtx)
			},
			want: false,
		},
		{
			name:     "ObjectValueDetails keeps the resolved value",
			flagType: "object",
			evaluate: func(c *of.Client) (any, error) {
				details, err := c.ObjectValueDetails(context.Background(), "bool-flag", false, evalCtx)
				return details.Value, err
			},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := withLogLevel(t, log.LevelDebug)

			p := newDatadogProvider(ProviderConfig{})
			p.updateConfiguration(createTestConfig())
			domain := "debug-logging-after-hook-failure-" + tc.flagType
			registerDebugLoggingProvider(t, domain, p)

			client := of.NewClient(domain)
			client.AddHooks(failingAfterHook{})
			value, err := tc.evaluate(client)
			require.ErrorContains(t, err, "application after hook failed")
			require.Equal(t, tc.want, value)

			logger.assertContains(t, `openfeature: provider resolved "bool-flag": type=`+tc.flagType+`, value=true, default=false, variant="on", reason=TARGETING_MATCH, allocation="allocation1", serial_id=, error_code=, error_message=""`)
			logger.assertContains(t, `openfeature: evaluation of "bool-flag" failed: after hook: application after hook failed (default value false, targeting_key="user-1", attributes={"country":"US"})`)
			assertNoReturnedValueClaims(t, logger, "bool-flag")
		})
	}
}

// newDebugLoggingMultiProviderClient returns a client for a first-match
// multi-provider that asks the Datadog provider, with ddHooks, before an
// in-memory provider serving "only-in-mem".
func newDebugLoggingMultiProviderClient(t *testing.T, ddHooks ...of.Hook) *of.Client {
	t.Helper()
	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())
	mem := memprovider.NewInMemoryProvider(map[string]memprovider.InMemoryFlag{
		"only-in-mem": {Key: "only-in-mem", State: memprovider.Enabled, DefaultVariant: "on", Variants: map[string]any{"on": true}},
	})
	mp, err := multi.NewProvider(multi.StrategyFirstMatch,
		multi.WithProvider("datadog", p, ddHooks...),
		multi.WithProvider("memory", mem),
	)
	require.NoError(t, err)

	api := isolated.NewAPI()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, api.Shutdown(ctx))
		assert.NoError(t, p.ShutdownWithContext(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, api.SetProviderAndWait(ctx, mp))
	return api.NewClient()
}

// TestDebugLogging_MultiProvider covers the Datadog provider inside a
// multi-provider, which can return a value other than the default after the
// provider's hooks saw a failure.
func TestDebugLogging_MultiProvider(t *testing.T) {
	t.Run("falls through to another provider", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		client := newDebugLoggingMultiProviderClient(t)

		value, err := client.BooleanValue(context.Background(), "only-in-mem", false, of.NewEvaluationContext("user-1", nil))
		require.NoError(t, err)
		require.True(t, value, "the in-memory provider serves the flag")

		logger.assertContains(t, `openfeature: evaluation of "only-in-mem" failed: error code: FLAG_NOT_FOUND: flag not found: "only-in-mem" (default value false, targeting_key="user-1", attributes={})`)
		assertNoReturnedValueClaims(t, logger, "only-in-mem")
	})

	t.Run("returns the resolved value after a failing After hook", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		client := newDebugLoggingMultiProviderClient(t, failingAfterHook{})

		value, err := client.BooleanValue(context.Background(), "bool-flag", false, of.NewEvaluationContext("user-1", map[string]any{"country": "US"}))
		require.NoError(t, err)
		require.True(t, value)

		logger.assertContains(t, `openfeature: provider resolved "bool-flag": type=bool, value=true, default=false, variant="on", reason=TARGETING_MATCH`)
		logger.assertContains(t, `openfeature: evaluation of "bool-flag" failed: after hook: application after hook failed (default value false, targeting_key="user-1", attributes={"country":"US"})`)
		assertNoReturnedValueClaims(t, logger, "bool-flag")
	})
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

		logger.assertContains(t, "openfeature: agentless configuration poll attempt 1/3 returned HTTP 200")
		// A custom endpoint never receives DD_ENV, so the configuration's
		// environment can differ from the SDK's.
		logger.assertContains(t, `openfeature: agentless configuration applied successfully (0 flag(s), 0 rejected, sdk_env=staging, requested_env=none (custom endpoint), config_env=Test, created_at=2024-04-17T19:40:53.716Z, etag="ufc-v1")`)
	})

	t.Run("applied configuration without DD_ENV", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("valid")

		newAgentlessSourceOnly(t, backend, time.Hour, func(*universalFlagsConfiguration) {}).poll()

		logger.assertContains(t, "openfeature: agentless configuration applied successfully (0 flag(s), 0 rejected, sdk_env=unset, ")
	})

	t.Run("unchanged configuration", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		backend := newFakeUFCBackend(t)
		backend.setResponses("not_modified")

		newDebugAgentlessSource(t, backend.server.URL).poll()

		logger.assertContains(t, "openfeature: agentless configuration poll attempt 1/3 returned HTTP 304")
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

	t.Run("DD_TRACE_DEBUG does not change the shared log level during provider construction", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_TRACE_DEBUG", "true")
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

		_, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)

		assert.False(t, log.DebugEnabled())
		assert.Empty(t, logger.all())
	})

	t.Run("without DD_TRACE_DEBUG the level is left alone", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		t.Setenv("DD_TRACE_DEBUG", "false")
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
	logger.assertContains(t, `openfeature: remote_config configuration "`+path+`" applied successfully (`+strconv.Itoa(len(cfg.Flags))+` flag(s), 0 rejected, sdk_env=`+cmp.Or(internalconfig.Get().Env(), "unset")+`, config_env=test, created_at=`+formatCreatedAt(cfg.CreatedAt)+`)`)
	logger.assertContains(t, `openfeature: remote_config configuration "`+path+`" removed`)
}

// panickingValue panics from every method a formatter could call, standing in
// for a caller-supplied default with a buggy MarshalJSON or String method.
type panickingValue struct{}

func (panickingValue) MarshalJSON() ([]byte, error) { panic("buggy MarshalJSON") }
func (panickingValue) String() string               { panic("buggy String") }

type panickingError struct{}

func (panickingError) Error() string { panic("Error must not be called") }

type namedString string

// TestDebugLoggingHook_ContainsMarshalerPanics pins that debug mode cannot
// turn a normal fallback into a panic: the SDK does not recover hook panics.
func TestDebugLoggingHook_ContainsMarshalerPanics(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)

	p := newDatadogProvider(ProviderConfig{})
	p.updateConfiguration(createTestConfig())
	const domain = "debug-logging-hook-panic"
	registerDebugLoggingProvider(t, domain, p)

	client := of.NewClient(domain)
	var value any
	require.NotPanics(t, func() {
		value, _ = client.ObjectValue(context.Background(), "does-not-exist", panickingValue{}, of.NewEvaluationContext("user-1", nil))
	})
	assert.Equal(t, panickingValue{}, value, "the caller's default is still returned")
	logger.assertContains(t, `provider resolved "does-not-exist": type=object, value=<unserializable>, default=<unserializable>, variant="", reason=ERROR`)
	logger.assertContains(t, `evaluation of "does-not-exist" failed: error code: FLAG_NOT_FOUND: flag not found: "does-not-exist" (default value <unserializable>, `)
}

func TestDebugLoggingHook_ContainsPanics(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	hookCtx := of.NewHookContext("my-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{}, of.EvaluationContext{})

	require.NotPanics(t, func() {
		newDebugLoggingHook(nil).Error(context.Background(), hookCtx, panickingError{}, of.HookHints{})
	})
	assert.Empty(t, logger.all(), "recovery logs nothing: the logger may be what panicked")
}

// TestDebugLoggingHook_LogsNotReadyEvaluations covers evaluating while the
// provider is still initializing: the SDK reports that only to Error hooks.
func TestDebugLoggingHook_LogsNotReadyEvaluations(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
	// Long enough that Init is still waiting when the evaluation runs.
	t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS", "5000")
	logger := withLogLevel(t, log.LevelDebug)

	p := newDatadogProvider(ProviderConfig{}) // no configuration: Init keeps waiting
	// An isolated API starts with no state. On the global API, a domain inherits
	// the default domain's state, which other tests leave READY.
	api := isolated.NewAPI()
	require.NoError(t, api.SetProvider(context.Background(), p))
	t.Cleanup(func() { _ = api.Shutdown(context.Background()) })

	client := api.NewClient()
	require.Equal(t, of.NotReadyState, client.State())
	_, err := client.BooleanValue(context.Background(), "early-flag", false, of.NewEvaluationContext("user-1", nil))
	require.Error(t, err)

	logger.assertContains(t, `openfeature: evaluation of "early-flag" failed: PROVIDER_NOT_READY: provider not yet initialized (default value false, targeting_key="user-1", attributes={})`)
	assert.Zero(t, logger.countContaining(`provider resolved "early-flag"`), "the SDK does not call the provider before it is ready")
}

// TestDebugLogging_EvaluationInputs checks the complete log stream of real
// evaluations, every registered hook and writer included. DD_TRACE_DEBUG
// exposes the raw targeting key and attributes so a customer can explain a
// result; with debug logging off, no line carries them.
func TestDebugLogging_EvaluationInputs(t *testing.T) {
	const (
		subject   = "subject-42"
		attrValue = "attr@example.com"
	)
	evaluate := func(t *testing.T, lvl log.Level) *capturingLogger {
		logger := withLogLevel(t, lvl)
		p := newDatadogProvider(ProviderConfig{})
		p.updateConfiguration(createTestConfig())
		domain := "debug-logging-evaluation-inputs-" + lvl.String()
		registerDebugLoggingProvider(t, domain, p)

		client := of.NewClient(domain)
		evalCtx := of.NewEvaluationContext(subject, map[string]any{"country": "US", "email": attrValue})
		on, err := client.BooleanValue(context.Background(), "bool-flag", false, evalCtx)
		require.NoError(t, err)
		require.True(t, on)
		_, _ = client.StringValue(context.Background(), "string-flag", "dflt", evalCtx)
		_, _ = client.BooleanValue(context.Background(), "does-not-exist", false, evalCtx)
		return logger
	}

	t.Run("debug", func(t *testing.T) {
		logger := evaluate(t, log.LevelDebug)
		inputs := `targeting_key="` + subject + `", attributes={"country":"US","email":"` + attrValue + `"}`
		logger.assertContains(t, `provider resolved "bool-flag": type=bool, value=true, default=false, `)
		logger.assertContains(t, `provider resolved "string-flag": type=string, value="dflt", default="dflt", `)
		assert.Equal(t, 4, logger.countContaining(inputs), "every provider resolution and failure logs its inputs")
		logger.assertContains(t, `evaluation of "does-not-exist" failed: error code: FLAG_NOT_FOUND: flag not found: "does-not-exist" (default value false, `+inputs+`)`)
	})

	t.Run("debug disabled", func(t *testing.T) {
		logger := evaluate(t, log.LevelWarn)
		for _, msg := range logger.all() {
			assert.NotContains(t, msg, subject)
			assert.NotContains(t, msg, attrValue)
		}
	})
}

// TestExposure_DebugLogs covers the exposure lifecycle: queued, suppressed
// with its reason, and the final shutdown flush outcome.
func TestExposure_DebugLogs(t *testing.T) {
	newProvider := func(t *testing.T, status int) *DatadogProvider {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == exposureEndpoint {
				w.WriteHeader(status)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		t.Cleanup(srv.Close)
		evp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
		return newDatadogProviderWithSourceAndEVP(ProviderConfig{ExposureFlushInterval: time.Hour}, internalffe.SourceAgentless, evp)
	}
	expose := func(p *DatadogProvider, subject string) {
		hookCtx := of.NewHookContext("my-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{Name: datadogProviderName}, of.NewEvaluationContext(subject, nil))
		details := of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
			FlagKey:  "my-flag",
			FlagType: of.Boolean,
			ResolutionDetail: of.ResolutionDetail{
				Variant:      "on",
				FlagMetadata: of.FlagMetadata{metadataAllocationKey: "alloc-1", metadataDoLogKey: true},
			},
		}}
		newExposureHook(p.exposureWriter).Finally(context.Background(), hookCtx, details, of.HookHints{})
	}
	shutdown := func(t *testing.T, p *DatadogProvider) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, p.ShutdownWithContext(ctx))
	}

	t.Run("queued, duplicate, and sent at shutdown", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newProvider(t, http.StatusAccepted)
		expose(p, "user-1")
		expose(p, "user-1")
		shutdown(t, p)
		expose(p, "user-2")

		logger.assertContains(t, `openfeature: queued exposure event for flag "my-flag" (allocation=alloc-1, variant=on, subject=user-1)`)
		logger.assertContains(t, `openfeature: skipping exposure event (duplicate of an earlier exposure for this subject, allocation, and variant) for flag "my-flag" (subject=user-1)`)
		logger.assertContains(t, "openfeature: shutdown sent 1 pending exposure event(s)")
		logger.assertContains(t, `openfeature: skipping exposure event (exposure writer stopped) for flag "my-flag"`)
	})

	t.Run("dropped at shutdown when delivery fails", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newProvider(t, http.StatusInternalServerError)
		expose(p, "user-1")
		shutdown(t, p)

		logger.assertContains(t, "openfeature: shutdown dropped 1 pending exposure event(s): final delivery failed: ")
	})

	t.Run("dropped by a periodic flush when delivery fails", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newProvider(t, http.StatusInternalServerError)
		p.exposureWriter.flushInterval = time.Millisecond
		p.exposureWriter.start()
		expose(p, "user-1")

		assert.Eventually(t, func() bool {
			return logger.countContaining("openfeature: dropped 1 exposure event(s) after failed delivery: ") > 0
		}, 5*time.Second, 5*time.Millisecond)
		shutdown(t, p)
	})

	t.Run("nothing pending at shutdown", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		shutdown(t, newProvider(t, http.StatusAccepted))

		logger.assertContains(t, "openfeature: shutdown found no pending exposure events to flush")
	})

	t.Run("silent when debug logging is disabled", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		p := newProvider(t, http.StatusAccepted)
		expose(p, "user-1")
		expose(p, "user-1")
		shutdown(t, p)

		assert.Empty(t, logger.all())
	})
}

func TestAgentlessSource_MalformedConfigurationDebugLogs(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	backend := newFakeUFCBackend(t)
	backend.setResponses("malformed")
	src := newDebugAgentlessSource(t, backend.server.URL)

	src.poll()
	src.poll()

	// Unlike the warning, the debug line fires on every malformed response.
	assert.Equal(t, 2, logger.countContaining("openfeature: agentless configuration poll attempt 1/3 returned malformed configuration: "))
	logger.assertContains(t, "; keeping the last known configuration")
	assert.Equal(t, 1, logger.countContaining("openfeature: agentless: received malformed configuration"))
}

func TestAgentlessSource_RequestedEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		managed bool
		env     string
		want    string
	}{
		{"custom endpoint never sends dd_env", false, "production", "none (custom endpoint)"},
		{"managed endpoint without DD_ENV", true, "", "none"},
		{"managed endpoint", true, "production", "production"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &agentlessSource{managed: tc.managed, env: tc.env}
			assert.Equal(t, tc.want, src.requestedEnv())
		})
	}

	src, err := newAgentlessSource(internalffe.Settings{APIKey: "key", Env: "production"}, func(*universalFlagsConfiguration) {})
	require.NoError(t, err)
	assert.True(t, src.managed)
	assert.Contains(t, src.endpoint, "dd_env=production", "requested_env must match what the managed endpoint receives")
}

const ufcWithRejectedFlag = `{
	"createdAt": "2026-10-01T12:00:00Z",
	"format": "SERVER",
	"environment": {"name": "Test"},
	"flags": {
		"good-flag": {"key": "good-flag", "enabled": true, "variationType": "BOOLEAN", "variations": {"on": {"key": "on", "value": true}}, "allocations": []},
		"bad-flag": {"key": "bad-flag", "enabled": true, "variationType": "NOT_A_TYPE", "variations": {}, "allocations": []}
	}
}`

func TestRejectedFlags_DebugLogs(t *testing.T) {
	t.Run("agentless", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"v7"`)
			_, _ = w.Write([]byte(`{"data":{"id":"1","type":"universal-flag-configuration","attributes":` + ufcWithRejectedFlag + `}}`))
		}))
		t.Cleanup(srv.Close)

		newDebugAgentlessSource(t, srv.URL).poll()

		logger.assertContains(t, `agentless configuration applied successfully (1 flag(s), 1 rejected, sdk_env=staging, requested_env=none (custom endpoint), config_env=Test, created_at=2026-10-01T12:00:00Z, etag="v7")`)
		logger.assertContains(t, `openfeature: agentless configuration rejected flag "bad-flag": `)
		assert.Zero(t, logger.countContaining(`rejected flag "good-flag"`))
	})

	t.Run("remote_config", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newDatadogProvider(ProviderConfig{})

		processConfigUpdate(p, "path/config", []byte(ufcWithRejectedFlag))

		logger.assertContains(t, `remote_config configuration "path/config" applied successfully (1 flag(s), 1 rejected, sdk_env=`+cmp.Or(internalconfig.Get().Env(), "unset")+`, config_env=Test, created_at=2026-10-01T12:00:00Z)`)
		logger.assertContains(t, `openfeature: remote_config configuration "path/config" rejected flag "bad-flag": `)
		assert.Zero(t, logger.countContaining(`rejected flag "good-flag"`))
	})
}

func TestLifecycle_DebugLogs(t *testing.T) {
	t.Run("first configuration before Init returns is not a recovery", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newDatadogProvider(ProviderConfig{})
		p.updateConfiguration(createTestConfig())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, p.InitWithContext(ctx, of.EvaluationContext{}))

		assert.Zero(t, logger.countContaining("provider recovered"))
	})

	t.Run("recovery, staleness, and shutdown", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newDatadogProvider(ProviderConfig{})

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		require.Error(t, p.InitWithContext(ctx, of.EvaluationContext{}))

		p.updateConfiguration(createTestConfig())
		logger.assertContains(t, "openfeature: provider recovered: configuration received, provider is ready")

		p.updateConfiguration(nil)
		logger.assertContains(t, "openfeature: provider is stale: configuration is unavailable")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		require.NoError(t, p.ShutdownWithContext(shutdownCtx))
		logger.assertContains(t, "openfeature: provider shutting down")
		logger.assertContains(t, "openfeature: provider shut down")
	})
}

func TestDebugLogging_SerialIDAndDisabledError(t *testing.T) {
	logger := withLogLevel(t, log.LevelDebug)
	hookCtx := of.NewHookContext("my-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{}, of.EvaluationContext{})

	logResolution(nil, "my-flag", of.Boolean, false, nil, of.BoolResolutionDetail{
		Value:                    true,
		ProviderResolutionDetail: of.ProviderResolutionDetail{FlagMetadata: of.FlagMetadata{metadataSplitSerialIDKey: uint32(7)}},
	})
	logger.assertContains(t, "serial_id=7,")

	newDebugLoggingHook(nil).Error(context.Background(), hookCtx, nil, of.HookHints{})
	log.SetLevel(log.LevelWarn)
	newDebugLoggingHook(nil).Error(context.Background(), hookCtx, errors.New("boom"), of.HookHints{})
	assert.Zero(t, logger.countContaining("failed:"), "no Error line for a nil error or with debug disabled")
}

func TestConfigurationIdentity_Unset(t *testing.T) {
	assert.Equal(t, "unset", formatCreatedAt(time.Time{}))

	logger := withLogLevel(t, log.LevelDebug)
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid_no_etag")
	newDebugAgentlessSource(t, backend.server.URL).poll()
	logger.assertContains(t, "etag=none)")
}

// stallingLogger takes p.mu for reading at the "provider shutting down" line,
// so the teardown, which needs p.mu, cannot finish until the test releases it.
type stallingLogger struct {
	capturingLogger
	p *DatadogProvider
}

func (l *stallingLogger) Log(msg string) {
	if strings.Contains(msg, "provider shutting down") {
		l.p.mu.RLock()
	}
	l.capturingLogger.Log(msg)
}

func TestShutdown_DebugLogsWhenContextExpires(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	logger := &stallingLogger{p: p}
	prev := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	undo := log.UseLogger(logger)
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := p.ShutdownWithContext(ctx)
	p.mu.RUnlock() // let the stalled teardown finish
	require.ErrorIs(t, err, context.DeadlineExceeded)

	logger.assertContains(t, "openfeature: provider shutdown did not finish before the context expired: context deadline exceeded")
}

// lockProbingLogger records, for each message, whether p.mu was free when it
// was logged. An application logger may evaluate a flag, which needs p.mu, so
// logging while p.mu is held can deadlock.
type lockProbingLogger struct {
	p         *DatadogProvider
	mu        sync.Mutex
	logged    []string
	underLock []string
}

func (l *lockProbingLogger) Log(msg string) {
	free := l.p.mu.TryRLock()
	if free {
		l.p.mu.RUnlock()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logged = append(l.logged, msg)
	if !free {
		l.underLock = append(l.underLock, msg)
	}
}

func (l *lockProbingLogger) assertLoggedWithoutLock(t *testing.T, substr string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	assert.True(t, slices.ContainsFunc(l.logged, func(m string) bool { return strings.Contains(m, substr) }),
		"expected a log containing %q, got:\n%s", substr, strings.Join(l.logged, "\n"))
	assert.False(t, slices.ContainsFunc(l.underLock, func(m string) bool { return strings.Contains(m, substr) }),
		"%q was logged while p.mu was held", substr)
}

func TestLifecycle_DebugLogsReleaseProviderLock(t *testing.T) {
	p := newDatadogProvider(ProviderConfig{})
	logger := &lockProbingLogger{p: p}
	prev := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	undo := log.UseLogger(logger)
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})

	initCtx, initCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer initCancel()
	require.Error(t, p.InitWithContext(initCtx, of.EvaluationContext{}))
	p.updateConfiguration(createTestConfig())
	p.updateConfiguration(nil)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	require.NoError(t, p.ShutdownWithContext(shutdownCtx))

	logger.assertLoggedWithoutLock(t, "provider recovered")
	logger.assertLoggedWithoutLock(t, "provider is stale")
	logger.assertLoggedWithoutLock(t, "provider shutting down")
	logger.assertLoggedWithoutLock(t, "shutdown found no pending exposure events")
}

type panickingLogger struct{}

func (panickingLogger) Log(string) { panic("logger failure") }

func TestDebugLoggingHook_ContainsLoggerPanics(t *testing.T) {
	prev := log.GetLevel()
	log.SetLevel(log.LevelDebug)
	undo := log.UseLogger(panickingLogger{})
	t.Cleanup(func() {
		undo()
		log.SetLevel(prev)
	})
	hookCtx := of.NewHookContext("my-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{}, of.EvaluationContext{})
	h := newDebugLoggingHook(new(atomic.Bool))

	assert.NotPanics(t, func() {
		logResolution(new(atomic.Bool), "my-flag", of.Boolean, false, of.FlattenedContext{of.TargetingKey: "user-1"}, of.BoolResolutionDetail{Value: true})
	})
	assert.NotPanics(t, func() {
		h.Error(context.Background(), hookCtx, of.ProviderNotReadyError, of.HookHints{})
	})
}

// The onboarding diagnostics share the package prefix with older logs, so
// identify them by message rather than failing unrelated writer diagnostics.
func isOnboardingLog(msg string) bool {
	_, message, ok := strings.Cut(msg, "openfeature: ")
	if !ok {
		return false
	}
	return slices.ContainsFunc([]string{
		"configuration source is ",
		"debug logging is enabled, ",
		"feature flagging is disabled;",
		"starting agentless configuration source ",
		"starting remote_config configuration source ",
		"waiting for provider initialization",
		"provider ",
		"agentless configuration ",
		"remote_config configuration ",
		"evaluation of ",
		"evaluating flag ",
		"queued exposure event ",
		"skipping exposure event ",
		"dropped ",
		"shutdown ",
		"flag evaluation metrics did not shut down",
	}, func(prefix string) bool { return strings.HasPrefix(message, prefix) })
}

// Fail only the new diagnostics to distinguish them from existing logging.
type panickingOnboardingLogger struct {
	capturingLogger
}

func (l *panickingOnboardingLogger) Log(msg string) {
	l.capturingLogger.Log(msg)
	if isOnboardingLog(msg) {
		panic("onboarding logger failure")
	}
}

func withPanickingOnboardingLogger(t *testing.T) *panickingOnboardingLogger {
	t.Helper()
	withLogLevel(t, log.LevelDebug)
	l := &panickingOnboardingLogger{}
	undo := log.UseLogger(l)
	t.Cleanup(undo)
	return l
}

func TestInitWithContext_ContainsLoggerPanics(t *testing.T) {
	for _, name := range []string{"success", "delivery error", "canceled", "timeout"} {
		t.Run(name, func(t *testing.T) {
			logger := withPanickingOnboardingLogger(t)
			p := &DatadogProvider{}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			var wantMessage string
			switch name {
			case "success":
				p.configuration = createTestConfig()
			case "delivery error":
				p.deliveryErr = errors.New("delivery unavailable")
				wantMessage = "no feature-flag delivery source could be started: delivery unavailable"
			case "canceled":
				cancel()
				wantMessage = "initialization was canceled before configuration arrived"
			case "timeout":
				wantMessage = "initialization timed out before configuration arrived"
			}

			var err error
			require.NotPanics(t, func() { err = p.InitWithContext(ctx, of.EvaluationContext{}) })
			if wantMessage == "" {
				require.NoError(t, err)
				logger.assertContains(t, "provider initialized successfully")
			} else {
				var initErr *of.ProviderInitError
				require.ErrorAs(t, err, &initErr)
				assert.Equal(t, of.ProviderNotReadyCode, initErr.ErrorCode)
				assert.Equal(t, wantMessage, initErr.Message)
				logger.assertContains(t, "provider failed to initialize")
			}
		})
	}
}

func TestAgentlessLifecycle_ContainsLoggerPanics(t *testing.T) {
	logger := withPanickingOnboardingLogger(t)
	backend := newFakeUFCBackend(t)
	var p *DatadogProvider
	require.NotPanics(t, func() {
		var err error
		p, err = startWithAgentless(ProviderConfig{}, internalffe.Settings{
			AgentlessBaseURL: backend.server.URL,
			PollInterval:     time.Hour,
			RequestTimeout:   time.Second,
		})
		require.NoError(t, err)
	})
	t.Cleanup(func() { p.Shutdown() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.InitWithContext(ctx, of.EvaluationContext{}))
	config := p.getConfiguration()
	require.NotNil(t, config)
	require.NotPanics(t, func() {
		p.updateConfiguration(nil)
		p.updateConfiguration(config)
	})
	logger.assertContains(t, "provider is stale")
	logger.assertContains(t, "provider recovered")

	require.NotPanics(t, func() { require.NoError(t, p.ShutdownWithContext(ctx)) })
	select {
	case <-p.agentless.doneCh:
	default:
		t.Fatal("shutdown did not stop the poller")
	}
	p.exposureWriter.mu.Lock()
	stopped := p.exposureWriter.stopped
	p.exposureWriter.mu.Unlock()
	assert.True(t, stopped, "shutdown must stop the exposure writer")
	assert.True(t, p.flagEvalLoggingWriter.stopped.Load(), "shutdown must stop the evaluation writer")
	logger.assertContains(t, "provider shutting down")
	logger.assertContains(t, "provider shut down")
}

func TestAgentlessPoll_ContainsLoggerPanics(t *testing.T) {
	logger := withPanickingOnboardingLogger(t)
	backend := newFakeUFCBackend(t)
	backend.setResponses("server_error", "valid", "not_modified")
	src := newDebugAgentlessSource(t, backend.server.URL)
	applied := 0
	src.apply = func(*universalFlagsConfiguration) { applied++ }

	require.NotPanics(t, src.poll)
	assert.Equal(t, 1, applied)
	assert.Equal(t, fakeUFCBackendETag, src.currentETag())
	logger.assertContains(t, "poll retrying in")
	logger.assertContains(t, "applied successfully")

	require.NotPanics(t, src.poll)
	assert.Equal(t, 1, applied, "an unchanged response must not reapply configuration")
	requests, _, etag, _ := backend.status()
	assert.Equal(t, 3, requests)
	assert.Equal(t, fakeUFCBackendETag, etag)
	logger.assertContains(t, "HTTP 304")
}

func TestEvaluationAndExposure_ContainLoggerPanics(t *testing.T) {
	logger := withPanickingOnboardingLogger(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	evp := testAgentlessEVPClient(t, nil, srv, "test-api-key")
	p := newDatadogProviderWithSourceAndEVP(ProviderConfig{ExposureFlushInterval: time.Hour}, internalffe.SourceAgentless, evp)

	var status rc.ApplyStatus
	require.NotPanics(t, func() { status = processConfigUpdate(p, "path/config", []byte(ufcWithRejectedFlag)) })
	assert.Equal(t, rc.ApplyStateAcknowledged, status.State)
	require.NotNil(t, p.getConfiguration(), "a failing logger must not skip applying configuration")
	logger.assertContains(t, `rejected flag "bad-flag"`)

	var detail of.BoolResolutionDetail
	require.NotPanics(t, func() {
		detail = p.BooleanEvaluation(context.Background(), "good-flag", false, of.FlattenedContext{of.TargetingKey: "user-1"})
	})
	// good-flag has no allocations, so the provider resolves its default.
	assert.Equal(t, of.DefaultReason, detail.Reason)
	assert.Equal(t, of.ResolutionError{}, detail.ResolutionError)
	logger.assertContains(t, `evaluating flag "good-flag"`)

	hookCtx := of.NewHookContext("good-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{Name: datadogProviderName}, of.NewEvaluationContext("user-1", nil))
	details := of.InterfaceEvaluationDetails{EvaluationDetails: of.EvaluationDetails{
		FlagKey:          "good-flag",
		ResolutionDetail: of.ResolutionDetail{Variant: "on", FlagMetadata: of.FlagMetadata{metadataAllocationKey: "alloc-1", metadataDoLogKey: true}},
	}}
	hook := newExposureHook(p.exposureWriter)
	for range 2 {
		require.NotPanics(t, func() { hook.Finally(context.Background(), hookCtx, details, of.HookHints{}) })
	}
	p.exposureWriter.mu.Lock()
	queued := len(p.exposureWriter.buffer)
	p.exposureWriter.mu.Unlock()
	assert.Equal(t, 1, queued, "a failing logger must not change what is queued")
	logger.assertContains(t, "skipping exposure event (duplicate")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NotPanics(t, func() { require.NoError(t, p.ShutdownWithContext(ctx)) })
	logger.assertContains(t, "shutdown sent 1 pending exposure event(s)")
	p.exposureWriter.mu.Lock()
	stopped := p.exposureWriter.stopped
	p.exposureWriter.mu.Unlock()
	assert.True(t, stopped, "a failing logger must not skip stopping the exposure writer")
}

func TestDebugLoggingHook_TracerStartupOrder(t *testing.T) {
	for _, tc := range []struct {
		name                                                                       string
		envDebug, tracerDebug, tracerFirst, wantHook, wantDebug, wantEvaluationLog bool
	}{
		{name: "provider_then_enable", tracerDebug: true, wantHook: true, wantDebug: true, wantEvaluationLog: true},
		{name: "provider_then_debug_disabled", wantHook: true},
		{name: "tracer_disable_then_provider", envDebug: true, tracerFirst: true, wantHook: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := withLogLevel(t, log.LevelWarn)
			t.Setenv("DD_TRACE_DEBUG", strconv.FormatBool(tc.envDebug))
			t.Setenv("DD_API_KEY", "")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
			t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
			t.Setenv("DD_RUNTIME_METRICS_ENABLED", "false")
			t.Setenv("DD_METRICS_OTEL_ENABLED", "false")
			t.Setenv("DD_APPSEC_ENABLED", "false")
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"endpoints":[]}`))
			}))
			defer agent.Close()
			t.Setenv("DD_TRACE_AGENT_URL", agent.URL)
			backend := newFakeUFCBackend(t)
			backend.setResponses("valid")
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "agentless")
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_BASE_URL", backend.server.URL)
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_POLL_INTERVAL_SECONDS", "3600")
			internalconfig.CreateNew()
			startTracer := func() {
				require.NoError(t, tracer.Start(tracer.WithDebugMode(tc.tracerDebug), tracer.WithLogStartup(false), tracer.WithHTTPClient(agent.Client())))
			}
			if tc.tracerFirst {
				startTracer()
			}
			defer tracer.Stop()
			provider, err := NewDatadogProvider(ProviderConfig{})
			require.NoError(t, err)
			p := provider.(*DatadogProvider)
			defer p.Shutdown()
			api := isolated.NewAPI()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, api.SetProviderAndWait(ctx, p))
			defer api.Shutdown(context.Background())
			if !tc.tracerFirst {
				startTracer()
			}
			value, err := api.NewClient().BooleanValue(context.Background(), "review-order-missing-flag", false, of.NewTargetlessEvaluationContext(nil))
			require.Error(t, err)
			require.False(t, value)
			hasHook := hasDebugLoggingHook(p.Hooks())
			evaluationLogged := logger.countContaining(`provider resolved "review-order-missing-flag"`) > 0
			require.Equal(t, tc.wantHook, hasHook)
			require.Equal(t, tc.wantDebug, log.DebugEnabled())
			require.Equal(t, tc.wantEvaluationLog, evaluationLogged)
			require.Equal(t, tc.tracerDebug, internalconfig.Get().Debug())
		})
	}
}

const rawDebugDataWarning = "openfeature: debug logging is enabled, so feature flag logs include raw evaluation data"

func (l *capturingLogger) indexContaining(substr string) int {
	return slices.IndexFunc(l.all(), func(msg string) bool { return strings.Contains(msg, substr) })
}

func findDebugLoggingHook(t *testing.T, p *DatadogProvider) *debugLoggingHook {
	t.Helper()
	for _, h := range p.Hooks() {
		if dh, ok := h.(*debugLoggingHook); ok {
			return dh
		}
	}
	t.Fatal("debug logging hook not registered")
	return nil
}

func TestRawDebugDataWarning(t *testing.T) {
	newConfiguredProvider := func(t *testing.T) *DatadogProvider {
		t.Helper()
		p := newDatadogProvider(ProviderConfig{})
		p.updateConfiguration(createTestConfig())
		t.Cleanup(p.Shutdown)
		return p
	}
	evaluate := func(p *DatadogProvider) of.BoolResolutionDetail {
		return p.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{of.TargetingKey: "user-1", "country": "US"})
	}
	hookCtx := of.NewHookContext("early-flag", of.Boolean, false, of.NewClientMetadata("test"), of.Metadata{}, of.NewEvaluationContext("user-1", nil))

	t.Run("warns once at construction when debug logging is enabled", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelDebug)
		p := newConfiguredProvider(t)
		require.Equal(t, 1, logger.countContaining(rawDebugDataWarning))
		logger.assertContains(t, "WARN: openfeature: debug logging is enabled, so feature flag logs include raw evaluation data (targeting keys, evaluation context attributes, flag values, and default values) that may contain personal or sensitive information. Unset DD_TRACE_DEBUG when you no longer need it.")

		evaluate(p)
		evaluate(p)
		findDebugLoggingHook(t, p).Error(context.Background(), hookCtx, of.ProviderNotReadyError, of.HookHints{})
		logger.assertContains(t, `provider resolved "bool-flag"`)
		logger.assertContains(t, `evaluation of "early-flag" failed`)
		assert.Equal(t, 1, logger.countContaining(rawDebugDataWarning), "evaluations must not repeat the warning")

		newConfiguredProvider(t)
		assert.Equal(t, 2, logger.countContaining(rawDebugDataWarning), "each provider warns once")
	})

	t.Run("silent when debug logging is disabled", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		p := newConfiguredProvider(t)
		evaluate(p)
		findDebugLoggingHook(t, p).Error(context.Background(), hookCtx, of.ProviderNotReadyError, of.HookHints{})
		assert.Zero(t, logger.countContaining(rawDebugDataWarning))
		assert.False(t, p.rawDataWarned.Load())
	})

	t.Run("silent when feature flagging is disabled", func(t *testing.T) {
		internalconfig.SetUseFreshConfig(true)
		t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
		logger := withLogLevel(t, log.LevelDebug)
		t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

		p, err := NewDatadogProvider(ProviderConfig{})
		require.NoError(t, err)
		require.IsType(t, &of.NoopProvider{}, p)
		logger.assertContains(t, "feature flagging is disabled")
		assert.Zero(t, logger.countContaining(rawDebugDataWarning))
	})

	t.Run("warns once before the first resolution when debug logging is enabled later", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		p := newConfiguredProvider(t)
		evaluate(p)
		require.Zero(t, logger.countContaining(rawDebugDataWarning))

		log.SetLevel(log.LevelDebug)
		evaluate(p)
		evaluate(p)
		findDebugLoggingHook(t, p).Error(context.Background(), hookCtx, of.ProviderNotReadyError, of.HookHints{})
		assert.Equal(t, 1, logger.countContaining(rawDebugDataWarning))
		firstRaw := logger.indexContaining(`provider resolved "bool-flag"`)
		require.GreaterOrEqual(t, firstRaw, 0)
		assert.Less(t, logger.indexContaining(rawDebugDataWarning), firstRaw)
	})

	t.Run("warns once before the first failure when debug logging is enabled later", func(t *testing.T) {
		logger := withLogLevel(t, log.LevelWarn)
		p := newDatadogProvider(ProviderConfig{}) // not ready: the SDK reports failures only to Error hooks

		log.SetLevel(log.LevelDebug)
		findDebugLoggingHook(t, p).Error(context.Background(), hookCtx, of.ProviderNotReadyError, of.HookHints{})
		p.BooleanEvaluation(context.Background(), "early-flag", false, of.FlattenedContext{of.TargetingKey: "user-1"})
		assert.Equal(t, 1, logger.countContaining(rawDebugDataWarning), "the hook and the provider share one warning")
		firstRaw := logger.indexContaining(`evaluation of "early-flag" failed`)
		require.GreaterOrEqual(t, firstRaw, 0)
		assert.Less(t, logger.indexContaining(rawDebugDataWarning), firstRaw)
	})

	t.Run("contains logger panics", func(t *testing.T) {
		logger := withPanickingOnboardingLogger(t)
		require.NotPanics(t, func() { newDatadogProvider(ProviderConfig{}) })

		log.SetLevel(log.LevelWarn)
		var p *DatadogProvider
		require.NotPanics(t, func() { p = newConfiguredProvider(t) })
		log.SetLevel(log.LevelDebug)
		var detail of.BoolResolutionDetail
		require.NotPanics(t, func() { detail = evaluate(p) })
		assert.True(t, detail.Value, "a failing logger must not change the resolution")
		assert.Equal(t, of.TargetingMatchReason, detail.Reason)
		assert.Equal(t, 2, logger.countContaining(rawDebugDataWarning))
	})
}
