// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	ddbaggage "github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestHttpDistributedTrace(t *testing.T) {
	assert := assert.New(t)
	tp, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tr := tp.Tracer("")

	sctx, rootSpan := tr.Start(context.Background(), "testRootSpan")

	w := otelhttp.NewHandler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		receivedSpan := oteltrace.SpanFromContext(r.Context())
		assert.Equal(rootSpan.SpanContext().TraceID(), receivedSpan.SpanContext().TraceID())
	}), "testOperation")
	testServer := httptest.NewServer(w)
	defer testServer.Close()
	c := http.Client{Transport: otelhttp.NewTransport(nil)}
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, testServer.URL, nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close()) // Need to close body to cause otel span to end
	rootSpan.End()

	p := <-payloads
	assert.Len(p, 2)
	expected := expectedSpanNames()
	assert.Equal(expected[0], p[0][0]["name"])
	assert.Equal(expected[1], p[1][0]["name"])
	assert.Equal(expected[2], p[1][1]["name"])
	assert.Equal("testOperation", p[0][0]["resource"])
	assert.Equal("testRootSpan", p[1][0]["resource"])
	assert.Equal("HTTP GET", p[1][1]["resource"])
}

func expectedSpanNames() []string {
	v := semver.MustParse(otelhttp.Version())
	if v.Compare(semver.MustParse("0.60.0")) <= 0 {
		return []string{"server.request", "internal", "client.request"}
	}
	return []string{"http.server.request", "internal", "http.client.request"}
}

// setupBaggageContext creates a context with both OpenTelemetry and Datadog baggage
func setupBaggageContext(t *testing.T) (context.Context, oteltrace.Span) {
	assert := assert.New(t)
	tp, _, cleanup := mockTracerProvider(t)
	t.Cleanup(cleanup)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.Baggage{})
	tr := tp.Tracer("")

	ctx := context.Background()
	otelMember, err := otelbaggage.NewMember("otel-key", "otel-value")
	assert.NoError(err)
	otelBag, err := otelbaggage.New(otelMember)
	assert.NoError(err)
	ctx = otelbaggage.ContextWithBaggage(ctx, otelBag)
	ctx = ddbaggage.Set(ctx, "dd-key", "dd-value")

	sctx, rootSpan := tr.Start(ctx, "testRootSpan")
	return sctx, rootSpan
}

// setupTestServer creates a test HTTP server that captures baggage headers
func setupTestServer(t *testing.T) (*httptest.Server, *string) {
	var extractedBaggage string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		extractedBaggage = r.Header.Get("baggage")
		w.WriteHeader(http.StatusOK)
	})
	wrappedHandler := otelhttp.NewHandler(handler, "testOperation")
	testServer := httptest.NewServer(wrappedHandler)
	t.Cleanup(testServer.Close)
	return testServer, &extractedBaggage
}

func TestHttpDistributedTraceWithBaggage(t *testing.T) {
	assert := assert.New(t)
	req := require.New(t)

	// Setup context with baggage
	sctx, rootSpan := setupBaggageContext(t)
	defer rootSpan.End()

	// Setup test server
	testServer, extractedBaggage := setupTestServer(t)

	// Make HTTP request with baggage context
	client := http.Client{Transport: otelhttp.NewTransport(nil)}
	httpReq, err := http.NewRequestWithContext(sctx, http.MethodGet, testServer.URL, nil)
	req.NoError(err)
	resp, err := client.Do(httpReq)
	req.NoError(err)
	req.NoError(resp.Body.Close())

	// Verify baggage propagation
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(sctx, carrier)

	// Check that the injected "baggage" header includes both baggage items
	injectedBaggage, ok := carrier["baggage"]
	req.True(ok, "baggage header must be present in the injected carrier")
	assert.Contains(injectedBaggage, "otel-key=otel-value")
	assert.Contains(injectedBaggage, "dd-key=dd-value")

	// Verify that the HTTP server received the merged baggage header
	assert.Contains(*extractedBaggage, "otel-key=otel-value")
	assert.Contains(*extractedBaggage, "dd-key=dd-value")
}
