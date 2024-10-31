// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestTraceEncoding(t *testing.T) {
	unregister := registerCodec()
	defer unregister()

	rig, err := newRig(true, WithServiceName("grpc"), WithRequestTags())
	require.NoError(t, err, "error setting up rig")
	defer func() { assert.NoError(t, rig.Close()) }()
	client := rig.client

	mt := mocktracer.Start()
	defer mt.Stop()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "root")
	_, err = client.Ping(ctx, &FixtureRequest{Name: "pass"})
	require.NoError(t, err)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 7)

	var (
		clientMarshalSpan   = spans[0]
		clientUnmarshalSpan = spans[1]
		serverSpan          = spans[2]
		serverMarshalSpan   = spans[3]
		serverUnmarshalSpan = spans[4]
		clientSpan          = spans[5]
		rootSpan            = spans[6]
	)
	assertParentAndChildSpans(t, rootSpan, clientSpan, "root and client")
	assertParentAndChildSpans(t, clientSpan, serverSpan, "client and server")

	assertSiblingSpans(t, clientSpan, clientMarshalSpan, "client and client.marshal")
	assertSiblingSpans(t, clientSpan, clientUnmarshalSpan, "client and client.unmarshal")

	assertSiblingSpans(t, serverSpan, serverMarshalSpan, "server and server.marshal")
	assertSiblingSpans(t, serverSpan, serverUnmarshalSpan, "server and server.unmarshal")
}

func assertParentAndChildSpans(t *testing.T, parent, child mocktracer.Span, msg string) {
	t.Helper()
	res := child.ParentID() == parent.SpanID() && child.TraceID() == parent.TraceID()
	assert.Truef(t, res, "%s: spans are not parent and child", msg)
}

func assertSiblingSpans(t *testing.T, s1, s2 mocktracer.Span, msg string) {
	t.Helper()
	res := s1.TraceID() == s2.TraceID()
	assert.Truef(t, res, "%s: spans are not siblings", msg)
}
