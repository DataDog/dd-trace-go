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

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	of "github.com/open-feature/go-sdk/openfeature"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/stretchr/testify/require"
)

func TestSemanticParityCDNAndRemoteConfigShareEvaluationResults(t *testing.T) {
	raw := mustMarshalUFC(t, createTestConfig())

	cdnProvider := newReadySemanticCDNProvider(t, raw)
	rcProvider := newSemanticRCProvider(t, raw)

	cases := []struct {
		name string
		eval func(*DatadogProvider) semanticSnapshot
	}{
		{
			name: "targeting match preserves value variant and reason",
			eval: func(provider *DatadogProvider) semanticSnapshot {
				detail := provider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
					of.TargetingKey: "user-123",
					"country":       "US",
				})
				return snapshotBool(detail)
			},
		},
		{
			name: "variant selection for string targeting is identical",
			eval: func(provider *DatadogProvider) semanticSnapshot {
				detail := provider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
					of.TargetingKey: "user-123",
					"age":           25,
				})
				return snapshotString(detail)
			},
		},
		{
			name: "default reason when targeting does not match is identical",
			eval: func(provider *DatadogProvider) semanticSnapshot {
				detail := provider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
					of.TargetingKey: "user-123",
					"age":           15,
				})
				return snapshotString(detail)
			},
		},
		{
			name: "missing flag error and default are identical",
			eval: func(provider *DatadogProvider) semanticSnapshot {
				detail := provider.BooleanEvaluation(context.Background(), "missing-flag", true, of.FlattenedContext{
					of.TargetingKey: "user-123",
				})
				return snapshotBool(detail)
			},
		},
		{
			name: "type mismatch error and default are identical",
			eval: func(provider *DatadogProvider) semanticSnapshot {
				detail := provider.BooleanEvaluation(context.Background(), "string-flag", true, of.FlattenedContext{
					of.TargetingKey: "user-123",
					"age":           25,
				})
				return snapshotBool(detail)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.eval(rcProvider), tc.eval(cdnProvider))
		})
	}
}

func TestSemanticCDNFailuresPreserveAcceptedEvaluatorState(t *testing.T) {
	t.Run("auth or site failure preserves accepted UFC state", func(t *testing.T) {
		internalffe.ResetForTest()
		t.Cleanup(internalffe.ResetForTest)
		t.Setenv(ffeProductEnvVar, "true")
		t.Setenv("DD_API_KEY", "test-api-key")

		var failAuth atomic.Bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if failAuth.Load() {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("ETag", `"ufc-valid"`)
			_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
		}))
		t.Cleanup(srv.Close)

		provider := newReadySemanticCDNProviderFromServer(t, srv)
		before := snapshotBool(provider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
			of.TargetingKey: "user-123",
			"country":       "US",
		}))

		failAuth.Store(true)
		require.Error(t, provider.cdnSource.poller.pollOnce(context.Background()))

		after := snapshotBool(provider.BooleanEvaluation(context.Background(), "bool-flag", false, of.FlattenedContext{
			of.TargetingKey: "user-123",
			"country":       "US",
		}))
		require.Equal(t, before, after)
	})

	t.Run("parse failure preserves accepted UFC state", func(t *testing.T) {
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

		provider := newReadySemanticCDNProviderFromServer(t, srv)
		before := snapshotString(provider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
			of.TargetingKey: "user-123",
			"age":           25,
		}))

		malformed.Store(true)
		require.Error(t, provider.cdnSource.poller.pollOnce(context.Background()))

		after := snapshotString(provider.StringEvaluation(context.Background(), "string-flag", "fallback", of.FlattenedContext{
			of.TargetingKey: "user-123",
			"age":           25,
		}))
		require.Equal(t, before, after)
	})
}

func TestSemanticShutdownIsIdempotentAndStopsCDNRequests(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("ETag", `"ufc-valid"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)

	provider := newReadySemanticCDNProviderFromServer(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, provider.ShutdownWithContext(ctx))
	require.NoError(t, provider.ShutdownWithContext(ctx))

	requestsAfterShutdown := requests.Load()
	require.Eventually(t, func() bool {
		return requests.Load() == requestsAfterShutdown
	}, 50*time.Millisecond, 10*time.Millisecond)
	require.Nil(t, provider.getConfiguration())
}

type semanticSnapshot struct {
	value   any
	variant string
	reason  of.Reason
	err     string
}

func snapshotBool(detail of.BoolResolutionDetail) semanticSnapshot {
	return semanticSnapshot{
		value:   detail.Value,
		variant: detail.Variant,
		reason:  detail.Reason,
		err:     resolutionErrorString(detail.ResolutionError),
	}
}

func snapshotString(detail of.StringResolutionDetail) semanticSnapshot {
	return semanticSnapshot{
		value:   detail.Value,
		variant: detail.Variant,
		reason:  detail.Reason,
		err:     resolutionErrorString(detail.ResolutionError),
	}
}

func resolutionErrorString(err of.ResolutionError) string {
	if err == (of.ResolutionError{}) {
		return ""
	}
	return err.Error()
}

func newReadySemanticCDNProvider(t *testing.T, raw []byte) *DatadogProvider {
	t.Helper()
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"ufc-valid"`)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return newReadySemanticCDNProviderFromServer(t, srv)
}

func newReadySemanticCDNProviderFromServer(t *testing.T, srv *httptest.Server) *DatadogProvider {
	t.Helper()
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
	return ddProvider
}

func newSemanticRCProvider(t *testing.T, raw []byte) *DatadogProvider {
	t.Helper()
	provider := newDatadogProvider(ProviderConfig{})
	status := processConfigUpdate(provider, "datadog/2/FFE_FLAGS/config", raw)
	require.Equal(t, rc.ApplyStateAcknowledged, status.State)
	return provider
}
