// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// findAttr returns the KeyValue with the given key from the slice, or nil if not found.
func findAttr(attrs []*otlpcommon.KeyValue, key string) *otlpcommon.KeyValue {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv
		}
	}
	return nil
}

// unmarshalPayload is a helper that pushes spans into a new payload, encodes it,
// and unmarshals it into a TracesData proto.
func unmarshalPayload(t *testing.T, c *config, spans ...*Span) *otlptrace.TracesData {
	t.Helper()
	p := newPayloadOTLP(c)
	_, err := p.push(spanList(spans))
	require.NoError(t, err)
	encoded, err := io.ReadAll(p)
	require.NoError(t, err)
	var td otlptrace.TracesData
	require.NoError(t, proto.Unmarshal(encoded, &td))
	return &td
}

func TestPayloadOTLPResourceAttributes(t *testing.T) {
	c, err := newTestConfig(
		WithService("my-service"),
		WithEnv("staging"),
		WithServiceVersion("1.2.3"),
	)
	require.NoError(t, err)

	td := unmarshalPayload(t, c, newSpan("op", "my-service", "res", 111, 222, 0))

	attrs := td.ResourceSpans[0].Resource.Attributes

	svcName := findAttr(attrs, "service.name")
	require.NotNil(t, svcName)
	assert.Equal(t, "my-service", svcName.Value.GetStringValue())

	env := findAttr(attrs, "deployment.environment")
	require.NotNil(t, env)
	assert.Equal(t, "staging", env.Value.GetStringValue())

	ver := findAttr(attrs, "service.version")
	require.NotNil(t, ver)
	assert.Equal(t, "1.2.3", ver.Value.GetStringValue())

	sdkLang := findAttr(attrs, "telemetry.sdk.language")
	require.NotNil(t, sdkLang)
	assert.Equal(t, "go", sdkLang.Value.GetStringValue())

	sdkName := findAttr(attrs, "telemetry.sdk.name")
	require.NotNil(t, sdkName)
	assert.Equal(t, "dd-trace-go", sdkName.Value.GetStringValue())
}

func TestPayloadOTLPScope(t *testing.T) {
	td := unmarshalPayload(t, nil, newSpan("op", "svc", "res", 111, 222, 0))

	require.Len(t, td.ResourceSpans[0].ScopeSpans, 1)
	scope := td.ResourceSpans[0].ScopeSpans[0].Scope
	require.NotNil(t, scope)
	assert.Equal(t, "dd-trace-go", scope.Name)
}

func TestPayloadOTLPSingleSpan(t *testing.T) {
	s := newSpan("op", "my-service", "my-resource", 111, 222, 0)
	s.start = 1000
	s.duration = 500
	s.meta[ext.SpanKind] = ext.SpanKindClient
	s.meta["http.method"] = "GET"
	s.metrics["http.status_code"] = 200.0
	s.error = 1
	s.meta[ext.ErrorMsg] = "connection refused"

	td := unmarshalPayload(t, nil, s)

	spans := td.ResourceSpans[0].ScopeSpans[0].Spans
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, "my-resource", span.Name)
	assert.Equal(t, otlptrace.Span_SPAN_KIND_CLIENT, span.Kind)
	assert.Equal(t, uint64(1000), span.StartTimeUnixNano)
	assert.Equal(t, uint64(1500), span.EndTimeUnixNano)

	require.NotNil(t, span.Status)
	assert.Equal(t, otlptrace.Status_STATUS_CODE_ERROR, span.Status.Code)
	assert.Equal(t, "connection refused", span.Status.Message)

	attr := findAttr(span.Attributes, "http.method")
	require.NotNil(t, attr)
	assert.Equal(t, "GET", attr.Value.GetStringValue())
}

func TestPayloadOTLPMultipleSpans(t *testing.T) {
	s1 := newSpan("op1", "svc", "res1", 1, 10, 0)
	s1.start = 100
	s1.duration = 50

	s2 := newSpan("op2", "svc", "res2", 2, 10, 1)
	s2.start = 200
	s2.duration = 75

	td := unmarshalPayload(t, nil, s1, s2)

	assert.Len(t, td.ResourceSpans[0].ScopeSpans[0].Spans, 2)
}

func TestPayloadOTLPReadIsIdempotent(t *testing.T) {
	s := newBasicSpan("op")
	p := newPayloadOTLP(nil)
	_, err := p.push(spanList{s})
	require.NoError(t, err)

	first, err := io.ReadAll(p)
	require.NoError(t, err)

	p.reset()

	second, err := io.ReadAll(p)
	require.NoError(t, err)

	assert.Equal(t, first, second, "reset should allow re-reading the same encoded bytes")
}

func TestPayloadOTLPWriteUnsupported(t *testing.T) {
	p := newPayloadOTLP(nil)
	n, err := p.Write([]byte("data"))
	assert.Equal(t, 0, n)
	assert.Error(t, err)
}
