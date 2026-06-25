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
	"sync/atomic"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	"github.com/stretchr/testify/require"
)

func TestSourceModeDefaultsToCDN(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv(envFeatureFlagSourceMode, "")
	t.Setenv(envFeatureFlagCDNPollIntervalSeconds, "60")
	t.Setenv(envFeatureFlagCDNRequestTimeoutSeconds, "1")
	t.Setenv("DD_API_KEY", "test-api-key")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, defaultCDNConfigPath, r.URL.Path)
		require.NotEmpty(t, r.Header.Get(cdnAPIKeyHeader))
		requests.Add(1)
		w.Header().Set("ETag", `"ufc-default"`)
		_, _ = w.Write(mustMarshalUFC(t, createTestConfig()))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envFeatureFlagCDNBaseURL, srv.URL)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{
				HTTPClient:     srv.Client(),
				PollInterval:   time.Hour,
				RequestTimeout: time.Second,
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

	require.Equal(t, FeatureFlagSourceModeCDN, ddProvider.sourceMode)
	require.NotNil(t, ddProvider.cdnSource)
	require.False(t, ddProvider.remoteConfigStarted)
	require.Eventually(t, func() bool {
		return ddProvider.getConfiguration() != nil
	}, time.Second, 10*time.Millisecond)
	require.Positive(t, requests.Load())
}

func TestSourceModeRemoteConfigOptInUsesRCAndDoesNotStartCDN(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv(envFeatureFlagSourceMode, string(FeatureFlagSourceModeRemoteConfig))
	t.Setenv("DD_API_KEY", "test-api-key")

	var cdnRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cdnRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envFeatureFlagCDNBaseURL, srv.URL)

	internalffe.SetSubscribedForTest(true)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{
		"datadog/2/FFE_FLAGS/config": mustMarshalUFC(t, createTestConfig()),
	})

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{HTTPClient: srv.Client()},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)

	require.Equal(t, FeatureFlagSourceModeRemoteConfig, ddProvider.sourceMode)
	require.Nil(t, ddProvider.cdnSource)
	require.True(t, ddProvider.remoteConfigStarted)
	require.NotNil(t, ddProvider.getConfiguration())
	require.Zero(t, cdnRequests.Load())
}

func TestSourceModeOfflineReservedDoesNotStartNetwork(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv("DD_API_KEY", "test-api-key")

	var cdnRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cdnRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envFeatureFlagCDNBaseURL, srv.URL)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			Mode: FeatureFlagSourceModeOffline,
			CDN:  FeatureFlagCDNConfig{HTTPClient: srv.Client()},
			Offline: FeatureFlagOfflineConfig{
				Payload: mustMarshalUFC(t, createTestConfig()),
			},
		},
	})
	require.NoError(t, err)
	ddProvider := requireDatadogProvider(t, provider)

	require.Equal(t, FeatureFlagSourceModeOffline, ddProvider.sourceMode)
	require.Nil(t, ddProvider.cdnSource)
	require.False(t, ddProvider.remoteConfigStarted)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, ddProvider.InitWithContext(ctx, of.EvaluationContext{}), context.DeadlineExceeded)
	require.Zero(t, cdnRequests.Load())
}

func TestSourceModeInvalidFailsClosed(t *testing.T) {
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Setenv(ffeProductEnvVar, "true")
	t.Setenv(envFeatureFlagSourceMode, "agent")
	t.Setenv("DD_API_KEY", "test-api-key")

	var cdnRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cdnRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envFeatureFlagCDNBaseURL, srv.URL)

	provider, err := NewDatadogProvider(ProviderConfig{
		Source: FeatureFlagSourceConfig{
			CDN: FeatureFlagCDNConfig{HTTPClient: srv.Client()},
		},
	})
	require.Error(t, err)
	require.Nil(t, provider)
	require.Zero(t, cdnRequests.Load())
}

func TestSourceModeNamesUseFinalSDKContract(t *testing.T) {
	require.Equal(t, "DD_FLAGGING_SOURCE_MODE", envFeatureFlagSourceMode)
	require.Equal(t, "DD_FLAGGING_CDN_BASE_URL", envFeatureFlagCDNBaseURL)
	require.Equal(t, "DD_FLAGGING_CDN_POLL_INTERVAL_SECONDS", envFeatureFlagCDNPollIntervalSeconds)
	require.Equal(t, "DD_FLAGGING_CDN_REQUEST_TIMEOUT_SECONDS", envFeatureFlagCDNRequestTimeoutSeconds)
	require.Equal(t, FeatureFlagSourceMode("cdn"), FeatureFlagSourceModeCDN)
	require.Equal(t, FeatureFlagSourceMode("remote_config"), FeatureFlagSourceModeRemoteConfig)
	require.Equal(t, FeatureFlagSourceMode("offline"), FeatureFlagSourceModeOffline)
	require.NotContains(t, envFeatureFlagSourceMode, "EXPERIMENTAL")
	require.NotContains(t, envFeatureFlagCDNBaseURL, "EXPERIMENTAL")
}

func requireDatadogProvider(t *testing.T, provider any) *DatadogProvider {
	t.Helper()
	ddProvider, ok := provider.(*DatadogProvider)
	require.True(t, ok, "expected *DatadogProvider, got %T", provider)
	return ddProvider
}

func mustMarshalUFC(t *testing.T, config *universalFlagsConfiguration) []byte {
	t.Helper()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	return data
}
