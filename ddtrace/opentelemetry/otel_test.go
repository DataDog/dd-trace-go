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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	otelbaggage "go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
	ddBaggage "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/baggage"
)

func TestHttpDistributedTrace(t *testing.T) {
	assert := assert.New(t)
	tp, payloads, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	tr := tp.Tracer("")

	sctx, rootSpan := tr.Start(context.Background(), "testRootSpan")

	w := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Equal("server.request", p[0][0]["name"])
	assert.Equal("internal", p[1][0]["name"])
	assert.Equal("client.request", p[1][1]["name"])
	assert.Equal("testOperation", p[0][0]["resource"])
	assert.Equal("testRootSpan", p[1][0]["resource"])
	assert.Equal("HTTP GET", p[1][1]["resource"])
}

func TestHttpDistributedTraceWithBaggage(t *testing.T) {
	assert := assert.New(t)
	req := require.New(t)
	tp, _, cleanup := mockTracerProvider(t)
	defer cleanup()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.Baggage{})
	tr := tp.Tracer("")
	ctx := context.Background()
	otelMember, _ := otelbaggage.NewMember("otel-key", "otel-value")
	otelBag, _ := otelbaggage.New(otelMember)
	ctx = otelbaggage.ContextWithBaggage(ctx, otelBag)
	ctx = ddBaggage.Set(ctx, "dd-key", "dd-value")
	sctx, rootSpan := tr.Start(ctx, "testRootSpan")
	defer rootSpan.End()

	var extractedBaggage string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the injected baggage header.
		extractedBaggage = r.Header.Get("baggage")
		w.WriteHeader(http.StatusOK)
	})
	wrappedHandler := otelhttp.NewHandler(handler, "testOperation")
	testServer := httptest.NewServer(wrappedHandler)
	defer testServer.Close()

	client := http.Client{Transport: otelhttp.NewTransport(nil)}
	httpReq, err := http.NewRequestWithContext(sctx, http.MethodGet, testServer.URL, nil)
	req.NoError(err)
	resp, err := client.Do(httpReq)
	req.NoError(err)
	req.NoError(resp.Body.Close())

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(sctx, carrier)

	// Check that the injected "baggage" header includes both baggage items.
	injectedBaggage, ok := carrier["baggage"]
	req.True(ok, "baggage header must be present in the injected carrier")
	assert.Contains(injectedBaggage, "otel-key=otel-value")
	assert.Contains(injectedBaggage, "dd-key=dd-value")

	// Also verify that the HTTP server received the merged baggage header.
	assert.Contains(extractedBaggage, "otel-key=otel-value")
	assert.Contains(extractedBaggage, "dd-key=dd-value")
}
