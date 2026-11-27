// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"testing"

	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseConfigPaths checks configs whose final state differs from their
// Config literal: one built without a literal, one whose Transport is set
// after the literal, and one whose CACert is set after the literal. The first
// two are traced. The third keeps its transport, so NewClient can still apply
// the certificate.
type TestCaseConfigPaths struct {
	addr      string
	transport countingTransport
}

func (tc *TestCaseConfigPaths) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion wraps the Config literal, so it misses the first two configs and breaks NewClient on the third")
	}
	tc.addr = startFake(t, nil)
}

func (tc *TestCaseConfigPaths) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	var noLiteral elasticsearch7.Config
	noLiteral.Addresses = []string{tc.addr}
	client, err := elasticsearch7.NewClient(noLiteral)
	require.NoError(t, err)
	perform(ctx, t, client, "v7-no-literal")

	transportAfter := elasticsearch7.Config{Addresses: []string{tc.addr}}
	transportAfter.Transport = &tc.transport
	client, err = elasticsearch7.NewClient(transportAfter)
	require.NoError(t, err)
	perform(ctx, t, client, "v7-transport-after-literal")
	require.EqualValues(t, 2, tc.transport.calls.Load(), "the GET / check and the request")

	_, caCert, _ := selfSigned(t)
	caCertAfter := elasticsearch8.Config{Addresses: []string{tc.addr}}
	caCertAfter.CACert = caCert
	v8, err := elasticsearch8.NewClient(caCertAfter)
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-ca-cert-after-literal")
}

// ExpectedSpanCount is test.root, an elasticsearch.query span and its net/http
// span for each v7 request and the GET / before each, and the net/http span
// of the untraced v8 request.
func (*TestCaseConfigPaths) ExpectedSpanCount() int {
	return 1 + 2*(2+2) + 1
}

func (*TestCaseConfigPaths) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				querySpan("v7-no-literal"),
				httpSpan("v7-no-literal"),
				querySpan("v7-transport-after-literal"),
				httpSpan("v7-transport-after-literal"),
				httpSpan("v8-ca-cert-after-literal"),
			},
		},
	}
}
