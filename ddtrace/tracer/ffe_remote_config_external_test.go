// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
