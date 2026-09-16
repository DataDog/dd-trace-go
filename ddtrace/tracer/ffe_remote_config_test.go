// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/require"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

type ffeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ffeRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFFERemoteConfigStartsAfterAgentInfoRecovers(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "true")
	remoteconfig.Reset()
	internalffe.ResetForTest()
	t.Cleanup(remoteconfig.Reset)
	t.Cleanup(internalffe.ResetForTest)

	var infoRecovered atomic.Bool
	rcRequests := make(chan string, 1)
	httpClient := &http.Client{Transport: ffeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/info" {
			if !infoRecovered.Load() {
				return nil, errors.New("connection reset by peer")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"endpoints":["/v0.7/config"]}`)),
			}, nil
		}
		if r.URL.Path == "/v0.7/config" {
			select {
			case rcRequests <- r.URL.String():
			default:
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}

	trc, err := newTracer(
		WithAgentURL("http://agent.test:8126"),
		WithHTTPClient(httpClient),
		withNoopStats(),
	)
	require.NoError(t, err)
	t.Cleanup(trc.Stop)

	trc.startAppSec()
	require.Empty(t, remoteconfig.ClientID(), "a transient /info failure should wait for positive Agent capability discovery")

	providerCallback := func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil }
	tracerOwnsSubscription, err := internalffe.SubscribeProvider(providerCallback)
	require.NoError(t, err)
	require.True(t, tracerOwnsSubscription, "the provider should wait for the tracer-owned RC client")
	require.True(t, internalffe.AttachCallback(providerCallback))

	infoRecovered.Store(true)
	trc.refreshAgentFeatures()

	found, err := remoteconfig.HasProduct(internalffe.FFEProductName)
	require.NoError(t, err)
	require.True(t, found, "FFE_FLAGS should be subscribed after Agent capability discovery recovers")

	select {
	case requestURL := <-rcRequests:
		require.Equal(t, "http://agent.test:8126/v0.7/config", requestURL)
	case <-time.After(2 * time.Second):
		t.Fatal("the tracer-owned RC client did not poll the resolved Agent URL")
	}
}

func TestFFERemoteConfigDoesNotStartWhenAgentIsKnownUnsupported(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "true")
	remoteconfig.Reset()
	internalffe.ResetForTest()
	t.Cleanup(remoteconfig.Reset)
	t.Cleanup(internalffe.ResetForTest)

	httpClient := &http.Client{Transport: ffeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/info" {
			t.Fatalf("unexpected request to unsupported Agent: %s", r.URL)
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}

	trc, err := newTracer(
		WithAgentURL("http://agent.test:8126"),
		WithHTTPClient(httpClient),
		withNoopStats(),
	)
	require.NoError(t, err)
	t.Cleanup(trc.Stop)

	trc.startAppSec()

	providerCallback := func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil }
	tracerOwnsSubscription, err := internalffe.SubscribeProvider(providerCallback)
	require.NoError(t, err)
	require.True(t, tracerOwnsSubscription, "the provider should not start a fallback client for a known-unsupported Agent")
	require.True(t, internalffe.AttachCallback(providerCallback))
	require.Empty(t, remoteconfig.ClientID(), "a conclusive unsupported response should not start RC")
}

func TestFFERemoteConfigExplicitDisableWinsWhenAgentInfoUnavailable(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
	remoteconfig.Reset()
	internalffe.ResetForTest()
	t.Cleanup(remoteconfig.Reset)
	t.Cleanup(internalffe.ResetForTest)

	httpClient := &http.Client{Transport: ffeRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/info" {
			t.Fatalf("unexpected request while RC is explicitly disabled: %s", r.URL)
		}
		return nil, errors.New("connection reset by peer")
	})}

	trc, err := newTracer(
		WithAgentURL("http://agent.test:8126"),
		WithHTTPClient(httpClient),
		withNoopStats(),
	)
	require.NoError(t, err)
	t.Cleanup(trc.Stop)

	trc.startAppSec()

	providerCallback := func(remoteconfig.ProductUpdate) map[string]rc.ApplyStatus { return nil }
	tracerOwnsSubscription, err := internalffe.SubscribeProvider(providerCallback)
	require.NoError(t, err)
	require.True(t, tracerOwnsSubscription, "the provider should not bypass explicit RC disablement with a fallback client")
	require.True(t, internalffe.AttachCallback(providerCallback))
	require.Empty(t, remoteconfig.ClientID(), "explicit RC disablement should prevent startup")
}

// TestFFERemoteConfigGating is the Go-side mirror of the parametric suite's
// _assert_no_ffe_remote_config_activation: the tracer must only subscribe to
// the FFE_FLAGS RC product when the resolved delivery source is remote_config.
func TestFFERemoteConfigGating(t *testing.T) {
	for name, tt := range map[string]struct {
		env            map[string]string
		wantSubscribed bool
	}{
		"default resolves to agentless, no RC subscription": {
			wantSubscribed: false,
		},
		"explicit source=agentless, no RC subscription": {
			env:            map[string]string{"DD_FEATURE_FLAGS_CONFIGURATION_SOURCE": "agentless"},
			wantSubscribed: false,
		},
		"explicit source=remote_config subscribes": {
			env:            map[string]string{"DD_FEATURE_FLAGS_CONFIGURATION_SOURCE": "remote_config"},
			wantSubscribed: true,
		},
		"legacy key true grandfathers remote_config": {
			env:            map[string]string{"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED": "true"},
			wantSubscribed: true,
		},
		"kill switch disables regardless of legacy key": {
			env: map[string]string{
				"DD_FEATURE_FLAGS_ENABLED":                  "false",
				"DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED": "true",
			},
			wantSubscribed: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			t.Cleanup(remoteconfig.Reset)
			t.Cleanup(remoteconfig.Stop)
			internalffe.ResetForTest()
			t.Cleanup(internalffe.ResetForTest)

			trc, _, _, stop, err := startTestTracer(t, WithService("my-service"), WithEnv("my-env"))
			require.NoError(t, err)
			t.Cleanup(stop)

			err = trc.startRemoteConfig(remoteconfig.DefaultClientConfig())
			require.NoError(t, err)

			found, err := remoteconfig.HasProduct(internalffe.FFEProductName)
			require.NoError(t, err)
			require.Equal(t, tt.wantSubscribed, found)
		})
	}
}
