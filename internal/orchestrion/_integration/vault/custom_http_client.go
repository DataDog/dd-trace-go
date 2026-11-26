// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package vault

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// calledRoundTripper records that a request went through it.
type calledRoundTripper struct {
	called *bool
}

func (rt *calledRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	*rt.called = true
	return http.DefaultTransport.RoundTrip(req)
}

// TestCaseCustomHTTPClient checks that a literal which sets its own
// HttpClient is traced once and keeps using that client's transport.
type TestCaseCustomHTTPClient struct {
	base
	rtCalled bool
}

func (tc *TestCaseCustomHTTPClient) Setup(_ context.Context, t *testing.T) {
	tc.base.setup(t)
	tc.base.newClient(t, &api.Config{
		Address:    tc.server.URL,
		HttpClient: &http.Client{Transport: &calledRoundTripper{called: &tc.rtCalled}},
	})
}

func (tc *TestCaseCustomHTTPClient) Run(ctx context.Context, t *testing.T) {
	tc.base.run(ctx, t)
	assert.True(t, tc.rtCalled, "the transport of the original HttpClient was not called")
}

func (*TestCaseCustomHTTPClient) ExpectedSpanCount() int {
	return spansPerRequest
}

func (tc *TestCaseCustomHTTPClient) ExpectedTraces() trace.Traces {
	return tc.base.expectedTraces()
}
