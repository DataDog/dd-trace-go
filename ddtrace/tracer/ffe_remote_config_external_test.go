// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
	ddof "github.com/DataDog/dd-trace-go/v2/openfeature"
)

type ffeRecoveryRoundTrip func(*http.Request) (*http.Response, error)

func (f ffeRecoveryRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFFEPendingProviderReplacementAfterTracerRestart(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "false")
	remoteconfig.Reset()
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Cleanup(remoteconfig.Reset)
	t.Cleanup(tracer.Stop)

	var recovered atomic.Bool
	client := &http.Client{Transport: ffeRecoveryRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		if r.URL.Path == "/info" && !recovered.Load() {
			return nil, errors.New("temporary failure")
		}
		body := `{}`
		if r.URL.Path == "/info" {
			body = `{"endpoints":["/v0.7/config"]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	start := func() {
		require.NoError(t, tracer.Start(tracer.WithAgentURL("http://agent.test:8126"), tracer.WithHTTPClient(client)))
	}
	start()
	provider, err := ddof.NewDatadogProvider(ddof.ProviderConfig{})
	require.NoError(t, err)
	provider.(*ddof.DatadogProvider).Shutdown()
	tracer.Stop()

	// No test-only resets between lifecycles: production shutdown must release
	// the pending callback even though discovery never created a subscription.
	recovered.Store(true)
	start()
	replacement, err := ddof.NewDatadogProvider(ddof.ProviderConfig{})
	require.NoError(t, err, "a replacement provider should attach after a full tracer restart")
	replacement.(*ddof.DatadogProvider).Shutdown()
}

func TestFFEProviderBecomesReadyAfterAgentRecovery(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "remote_config")
	t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "false")
	t.Setenv("DD_FLAGGING_EVALUATION_COUNTS_ENABLED", "false")
	remoteconfig.Reset()
	internalffe.ResetForTest()
	t.Cleanup(internalffe.ResetForTest)
	t.Cleanup(remoteconfig.Reset)
	t.Cleanup(tracer.Stop)
	t.Cleanup(of.Shutdown)

	payload := []byte(`{"format":"SERVER","flags":{"recovered":{"key":"recovered","enabled":true,"variationType":"BOOLEAN","variations":{"on":{"key":"on","value":true}},"allocations":[{"key":"all","doLog":false,"splits":[{"variationKey":"on","shards":[]}]}]}}}`)
	const path = "datadog/2/FFE_FLAGS/recovery/config"
	targets := fmt.Sprintf(`{"signed":{"_type":"targets","spec_version":"1.0.0","expires":"2099-01-01T00:00:00Z","version":1,"targets":{"%s":{"custom":{"v":1},"hashes":{"sha256":"%x"},"length":%d}}}}`, path, sha256.Sum256(payload), len(payload))
	response, err := json.Marshal(map[string]any{
		"targets":        []byte(targets),
		"target_files":   []map[string]any{{"path": path, "raw": payload}},
		"client_configs": []string{path},
	})
	require.NoError(t, err)

	var recovered atomic.Bool
	rcRequests := make(chan string, 1)
	httpClient := &http.Client{Transport: ffeRecoveryRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		body := `{}`
		switch r.URL.Path {
		case "/info":
			if !recovered.Load() {
				return nil, errors.New("temporary failure")
			}
			body = `{"endpoints":["/v0.7/config"]}`
		case "/v0.7/config":
			select {
			case rcRequests <- r.URL.String():
			default:
			}
			body = string(response)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	require.NoError(t, tracer.Start(tracer.WithAgentURL("http://agent.test:8126"), tracer.WithHTTPClient(httpClient)))
	provider, err := ddof.NewDatadogProvider(ddof.ProviderConfig{})
	require.NoError(t, err)
	t.Cleanup(provider.(*ddof.DatadogProvider).Shutdown)
	require.Empty(t, remoteconfig.ClientID(), "the provider must wait for Agent discovery")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, of.SetNamedProviderWithContext(ctx, t.Name(), provider))
	client := of.NewClient(t.Name())
	require.Equal(t, of.NotReadyState, client.State())
	recovered.Store(true)

	// Exercise the tracer's actual periodic discovery and the RC callback,
	// including the pending attachment made before any shared client existed.
	require.Eventually(t, func() bool { return client.State() == of.ReadyState }, 10*time.Second, 10*time.Millisecond)
	select {
	case requestURL := <-rcRequests:
		require.Equal(t, "http://agent.test:8126/v0.7/config", requestURL)
	default:
		t.Fatal("the provider became ready without an RC request")
	}
	value, err := client.BooleanValue(ctx, "recovered", false, of.NewEvaluationContext("user", nil))
	require.NoError(t, err)
	require.True(t, value, "the recovered provider must evaluate configuration rather than the caller's default")
}
