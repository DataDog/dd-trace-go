// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/stretchr/testify/require"
)

func TestOpenFeatureLifecycleColdStartIsBoundedAndDefaults(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				BaseURL:        srv.URL,
				PollInterval:   time.Hour,
				RequestTimeout: time.Second,
				HTTPClient:     srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 100*time.Millisecond)

	ddProvider := requireDatadogProvider(t, provider)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, ddProvider.ShutdownWithContext(ctx))
	})

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}), context.DeadlineExceeded)

	result := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", true, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"country":       "US",
	})
	require.True(t, result.Value)
	require.Equal(t, of.ErrorReason, result.Reason)
	require.Contains(t, result.ResolutionError.Error(), string(of.ProviderNotReadyCode))
}

func TestOpenFeatureCDNValidConfigBecomesReady(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, defaultCDNConfigPath, r.URL.Path)
		require.NotEmpty(t, r.Header.Get(cdnAPIKeyHeader))
		w.Header().Set("ETag", `"ufc-valid"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				BaseURL:        srv.URL,
				PollInterval:   time.Hour,
				RequestTimeout: time.Second,
				HTTPClient:     srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, ddProvider.ShutdownWithContext(ctx))
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}))

	result := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"country":       "US",
	})
	require.True(t, result.Value)
	require.Equal(t, "on", result.Variant)
	require.Equal(t, of.TargetingMatchReason, result.Reason)
	require.Empty(t, result.ResolutionError)
	require.Equal(t, FeatureFlagSourceModeCDN, ddProvider.sourceMode)
	require.False(t, ddProvider.remoteConfigStarted)
	require.Positive(t, requests.Load())
}

func TestOpenFeatureCDNAuthFailureFailsClosedAtProviderBoundary(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Empty(t, r.Header.Get(cdnAPIKeyHeader))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				BaseURL:        srv.URL,
				PollInterval:   time.Hour,
				RequestTimeout: time.Second,
				HTTPClient:     srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)
	ddProvider.cdnSource.poller.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, ddProvider.ShutdownWithContext(ctx))
	})

	require.Eventually(t, func() bool {
		return requests.Load() > 0
	}, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}), context.DeadlineExceeded)

	result := ddProvider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
		of.TargetingKey: "user-123",
		"age":           25,
	})
	require.Equal(t, "fallback", result.Value)
	require.Equal(t, of.ErrorReason, result.Reason)
	require.Contains(t, result.ResolutionError.Error(), string(of.ProviderNotReadyCode))
}

func TestOpenFeatureCDNTransientFailurePreservesLastKnownGood(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", `"ufc-lkg"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				BaseURL:        srv.URL,
				PollInterval:   time.Hour,
				RequestTimeout: time.Second,
				HTTPClient:     srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)
	ddProvider.cdnSource.poller.backoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, ddProvider.ShutdownWithContext(ctx))
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}))

	before := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"country":       "US",
	})
	require.True(t, before.Value)
	require.Equal(t, of.TargetingMatchReason, before.Reason)

	fail.Store(true)
	require.Error(t, ddProvider.cdnSource.poller.pollOnce(context.Background()))

	after := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"country":       "US",
	})
	require.Equal(t, before.Value, after.Value)
	require.Equal(t, before.Variant, after.Variant)
	require.Equal(t, before.Reason, after.Reason)
	require.Empty(t, after.ResolutionError)
}

func TestOpenFeatureCDNParseFailureFailsClosedAndPreservesWarmState(t *testing.T) {
	t.Run("malformed cold config returns default error", func(t *testing.T) {
		internalffe.ResetForTest()
		t.Cleanup(internalffe.ResetForTest)
		t.Setenv(ffeProductEnvVar, "true")
		t.Setenv("DD_API_KEY", "test-api-key")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"format":"SERVER","flags":{"broken":`))
		}))
		t.Cleanup(srv.Close)

		provider, err := NewDatadogProvider(ProviderConfig{
			Source: FeatureFlagSourceConfig{
				CDN: FeatureFlagCDNConfig{
					BaseURL:        srv.URL,
					PollInterval:   time.Hour,
					RequestTimeout: time.Second,
					HTTPClient:     srv.Client(),
				},
			},
		})
		require.NoError(t, err)
		ddProvider := requireDatadogProvider(t, provider)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, ddProvider.ShutdownWithContext(ctx))
		})

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		require.ErrorIs(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}), context.DeadlineExceeded)

		result := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
			of.TargetingKey: "user-123",
			"country":       "US",
		})
		require.False(t, result.Value)
		require.Equal(t, of.ErrorReason, result.Reason)
		require.Contains(t, result.ResolutionError.Error(), string(of.ProviderNotReadyCode))
	})

	t.Run("malformed warm config preserves accepted config", func(t *testing.T) {
		internalffe.ResetForTest()
		t.Cleanup(internalffe.ResetForTest)
		t.Setenv(ffeProductEnvVar, "true")
		t.Setenv("DD_API_KEY", "test-api-key")

		var malformed atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if malformed.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"format":"SERVER","flags":{"broken":`))
				return
			}
			w.Header().Set("ETag", `"ufc-valid"`)
			_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
		}))
		t.Cleanup(srv.Close)

		provider, err := NewDatadogProvider(ProviderConfig{
			Source: FeatureFlagSourceConfig{
				CDN: FeatureFlagCDNConfig{
					BaseURL:        srv.URL,
					PollInterval:   time.Hour,
					RequestTimeout: time.Second,
					HTTPClient:     srv.Client(),
				},
			},
		})
		require.NoError(t, err)
		ddProvider := requireDatadogProvider(t, provider)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, ddProvider.ShutdownWithContext(ctx))
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}))

		malformed.Store(true)
		require.Error(t, ddProvider.cdnSource.poller.pollOnce(context.Background()))

		result := ddProvider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
			of.TargetingKey: "user-123",
			"age":           25,
		})
		require.Equal(t, "version-2", result.Value)
		require.Equal(t, "v2", result.Variant)
		require.Equal(t, of.TargetingMatchReason, result.Reason)
		require.Empty(t, result.ResolutionError)
	})
}

func TestOpenFeatureLifecycleRemoteConfigSuppressesCDNPolling(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")

	var cdnRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cdnRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	internalffe.SetSubscribedForTest(true)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{
		"datadog/2/FFE_FLAGS/config": mustMarshalUFC(t, createTestConfig()),
	})

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			Mode: FeatureFlagSourceModeRemoteConfig,
			CDN: FeatureFlagCDNConfig{
				BaseURL:    srv.URL,
				HTTPClient: srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}))

	result := ddProvider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"country":       "US",
	})
	require.True(t, result.Value)
	require.Equal(t, "on", result.Variant)
	require.Equal(t, of.TargetingMatchReason, result.Reason)
	require.Nil(t, ddProvider.cdnSource)
	require.True(t, ddProvider.remoteConfigStarted)
	require.Zero(t, cdnRequests.Load())
}

func TestOpenFeatureLifecycleShutdownCancelsInFlightAndStopsRequests(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	started := make(chan struct{})
	cancelled := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-started:
		default:
			close(started)
		}
		<-r.Context().Done()
		select {
		case <-cancelled:
		default:
			close(cancelled)
		}
	}))
	t.Cleanup(srv.Close)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				BaseURL:        srv.URL,
				PollInterval:   10 * time.Millisecond,
				RequestTimeout: time.Minute,
				HTTPClient:     srv.Client(),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)

	require.Eventually(t, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, ddProvider.ShutdownWithContext(ctx))

	require.Eventually(t, func() bool {
		select {
		case <-cancelled:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	requestsAfterShutdown := requests.Load()
	require.Eventually(t, func() bool {
		return requests.Load() == requestsAfterShutdown
	}, 50*time.Millisecond, 10*time.Millisecond)
	require.Nil(t, ddProvider.getConfiguration())
}
