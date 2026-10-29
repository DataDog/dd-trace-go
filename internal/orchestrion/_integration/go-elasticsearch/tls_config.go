// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package go_elasticsearch

import (
	"context"
	"crypto/tls"
	"testing"

	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestCaseCACert checks that a v7 or v8 client with a CA certificate in its
// Config literal still connects. go-elasticsearch applies the certificate only
// to an *http.Transport, so the client is not traced.
type TestCaseCACert struct {
	addr   string
	caCert []byte
}

func (tc *TestCaseCACert) Setup(_ context.Context, t *testing.T) {
	var tlsConfig *tls.Config
	tlsConfig, tc.caCert, _ = selfSigned(t)
	tc.addr = startFake(t, tlsConfig)
}

func (tc *TestCaseCACert) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	v7, err := elasticsearch7.NewClient(elasticsearch7.Config{Addresses: []string{tc.addr}, CACert: tc.caCert})
	require.NoError(t, err)
	perform(ctx, t, v7, "v7-ca-cert")

	v8, err := elasticsearch8.NewClient(elasticsearch8.Config{Addresses: []string{tc.addr}, CACert: tc.caCert})
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-ca-cert")
}

// ExpectedSpanCount is test.root and the net/http spans of the v7 GET /, the
// v7 request and the v8 request.
func (*TestCaseCACert) ExpectedSpanCount() int {
	return 1 + 3
}

func (*TestCaseCACert) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{httpSpan("v7-ca-cert"), httpSpan("v8-ca-cert")},
		},
	}
}

// TestCaseFingerprint checks that a v8 client pinned to a certificate
// fingerprint still connects. go-elasticsearch checks the fingerprint only in
// an *http.Transport, so the client is not traced.
type TestCaseFingerprint struct {
	addr        string
	fingerprint string
}

func (tc *TestCaseFingerprint) Setup(_ context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion wraps the transport, so the fingerprint is not checked and the TLS handshake fails")
	}
	var tlsConfig *tls.Config
	tlsConfig, _, tc.fingerprint = selfSigned(t)
	tc.addr = startFake(t, tlsConfig)
}

func (tc *TestCaseFingerprint) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	v8, err := elasticsearch8.NewClient(elasticsearch8.Config{
		Addresses:              []string{tc.addr},
		CertificateFingerprint: tc.fingerprint,
	})
	require.NoError(t, err)
	perform(ctx, t, v8, "v8-fingerprint")
}

// ExpectedSpanCount is test.root and the net/http span of the request.
func (*TestCaseFingerprint) ExpectedSpanCount() int {
	return 1 + 1
}

func (*TestCaseFingerprint) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{httpSpan("v8-fingerprint")},
		},
	}
}
