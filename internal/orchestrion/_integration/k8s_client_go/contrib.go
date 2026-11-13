// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package k8sclientgo

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"

	kubernetestrace "github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2/kubernetes"
	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const contribAnalyticsRate = 0.5

// expectedTracesWithRate is the trace of base, with the analytics rate the
// contrib options set on the client-go span.
func (b *base) expectedTracesWithRate() trace.Traces {
	traces := b.expectedTraces()
	traces[0].Metrics = map[string]float64{ext.EventSampleRate: contribAnalyticsRate}
	return traces
}

// TestCaseContribWrapTransport checks that a literal whose WrapTransport
// already uses the contrib gets one client-go span, with the contrib's
// options. Orchestrion wraps the contrib with a second wrapper that drops
// those options, so it only runs under otelc.
type TestCaseContribWrapTransport struct {
	base
}

func (tc *TestCaseContribWrapTransport) Setup(ctx context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion drops the options of a WrapTransport that uses the contrib")
	}
	tc.base.setup(ctx, t)

	cfg := &rest.Config{
		Host:          tc.server.URL,
		WrapTransport: kubernetestrace.WrapRoundTripperFunc(httptrace.WithAnalyticsRate(contribAnalyticsRate)),
	}
	client, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCaseContribWrapTransport) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseContribWrapTransport) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseContribWrapTransport) ExpectedTraces() trace.Traces {
	return tc.base.expectedTracesWithRate()
}

// TestCaseContribWrapTransportChain checks that a WrapTransport which uses the
// contrib under another round tripper gets one client-go span. Orchestrion
// reports two, so it only runs under otelc.
type TestCaseContribWrapTransportChain struct {
	base
	rtCalled bool
}

// calledRoundTripper records that a request went through it.
type calledRoundTripper struct {
	base   http.RoundTripper
	called *bool
}

func (rt *calledRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	*rt.called = true
	return rt.base.RoundTrip(req)
}

func (tc *TestCaseContribWrapTransportChain) Setup(ctx context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion reports two client-go spans when the contrib is under another wrapper")
	}
	tc.base.setup(ctx, t)

	cfg := &rest.Config{
		Host: tc.server.URL,
		WrapTransport: transport.Wrappers(
			kubernetestrace.WrapRoundTripper,
			func(rt http.RoundTripper) http.RoundTripper {
				return &calledRoundTripper{base: rt, called: &tc.rtCalled}
			},
		),
	}
	client, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCaseContribWrapTransportChain) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
	assert.True(t, tc.rtCalled, "the round tripper of the original WrapTransport was not called")
}

func (*TestCaseContribWrapTransportChain) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseContribWrapTransportChain) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}

// TestCaseContribConfigWrap checks that a config from clientcmd, which the
// rules already wrap, gets one client-go span when the application adds the
// contrib with rest.Config.Wrap, and keeps the contrib's options.
type TestCaseContribConfigWrap struct {
	base
}

func (tc *TestCaseContribConfigWrap) Setup(ctx context.Context, t *testing.T) {
	tc.base.setup(ctx, t)

	cfg, err := clientcmd.BuildConfigFromKubeconfigGetter(tc.server.URL, func() (*clientcmdapi.Config, error) {
		return clientcmdapi.NewConfig(), nil
	})
	require.NoError(t, err)
	cfg.Wrap(kubernetestrace.WrapRoundTripperFunc(httptrace.WithAnalyticsRate(contribAnalyticsRate)))

	client, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	tc.base.client = client
}

func (tc *TestCaseContribConfigWrap) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
}

func (*TestCaseContribConfigWrap) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseContribConfigWrap) ExpectedTraces() trace.Traces {
	return tc.base.expectedTracesWithRate()
}
