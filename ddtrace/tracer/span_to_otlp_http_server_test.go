// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

func TestConvertSpanOTelSemanticHTTPServer(t *testing.T) {
	s := newSpan("http.request", "svc", "GET /users/{id}", 100, 200, 0)
	s.error = 1
	s.meta.Set(ext.SpanKind, ext.SpanKindServer)
	s.meta.Set(ext.HTTPRequestMethod, "GET")
	s.meta.Set(ext.HTTPResponseStatusCode, "500")
	s.meta.Set(ext.ErrorType, "500")
	s.metrics[ext.ServerPort] = 8443

	otlp := convertSpan(s, "svc", true)
	require.NotNil(t, otlp)
	assert.Equal(t, "GET /users/{id}", otlp.Name)
	assert.Equal(t, otlptrace.Span_SPAN_KIND_SERVER, otlp.Kind)
	assert.Equal(t, otlptrace.Status_STATUS_CODE_ERROR, otlp.Status.Code)

	attrs := keyValuesToMap(otlp.Attributes)
	assert.Equal(t, "GET", attrs[ext.HTTPRequestMethod])
	assert.Equal(t, int64(500), attrs[ext.HTTPResponseStatusCode])
	assert.Equal(t, int64(8443), attrs[ext.ServerPort])
	assert.Equal(t, "500", attrs[ext.ErrorType])
}
