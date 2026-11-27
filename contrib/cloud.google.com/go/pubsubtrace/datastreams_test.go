// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsubtrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	dsmTopicFull        = "projects/test-project/topics/test-topic"
	dsmSubscriptionFull = "projects/test-project/subscriptions/test-subscription"
)

func TestTracePublishDataStreams(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	msg := &Message{Data: []byte("hello"), Attributes: map[string]string{"order": "123"}}
	_, closeSpan := newTestTracer().TracePublish(context.Background(), resource(dsmTopicFull), msg)
	closeSpan("server-id", nil)

	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(), dataStreamsCarrier(msg.Attributes),
	))
	require.True(t, ok, "pathway not found in published message attributes")

	wantCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	require.True(t, ok)
	want, _ := datastreams.PathwayFromContext(wantCtx)
	assert.NotZero(t, want.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func TestTracePublishDataStreamsDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	msg := &Message{Data: []byte("hello")}
	_, closeSpan := newTestTracer().TracePublish(context.Background(), resource(dsmTopicFull), msg)
	closeSpan("server-id", nil)

	_, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(), dataStreamsCarrier(msg.Attributes),
	))
	assert.False(t, ok, "pathway must not be injected when data streams are disabled")
}

func TestTraceReceiveFuncDataStreams(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	producerCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	require.True(t, ok)

	msg := &Message{ID: "1", Data: []byte("hello"), Attributes: map[string]string{}}
	datastreams.InjectToBase64Carrier(producerCtx, dataStreamsCarrier(msg.Attributes))

	traceFn := newTestTracer().TraceReceiveFunc(resource(dsmSubscriptionFull))
	ctx, finish := traceFn(context.Background(), msg)
	finish()

	got, ok := datastreams.PathwayFromContext(ctx)
	require.True(t, ok, "pathway not found in receive handler context")

	wantCtx, ok := tracer.SetDataStreamsCheckpoint(producerCtx, consumeEdgeTags(resource(dsmSubscriptionFull))...)
	require.True(t, ok)
	want, _ := datastreams.PathwayFromContext(wantCtx)
	assert.NotZero(t, want.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func TestTraceReceiveFuncDataStreamsDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	producerCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	require.True(t, ok)

	msg := &Message{ID: "1", Data: []byte("hello"), Attributes: map[string]string{}}
	datastreams.InjectToBase64Carrier(producerCtx, dataStreamsCarrier(msg.Attributes))

	traceFn := newTestTracer().TraceReceiveFunc(resource(dsmSubscriptionFull))
	ctx, finish := traceFn(context.Background(), msg)
	finish()

	_, ok = datastreams.PathwayFromContext(ctx)
	assert.False(t, ok, "handler context must not carry a pathway when data streams are disabled")
}

func TestTraceReceiveFuncDataStreamsForwardChainsThroughContext(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	producerCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	require.True(t, ok)

	msg := &Message{ID: "1", Data: []byte("hello"), Attributes: map[string]string{}}
	datastreams.InjectToBase64Carrier(producerCtx, dataStreamsCarrier(msg.Attributes))

	traceFn := newTestTracer().TraceReceiveFunc(resource(dsmSubscriptionFull))
	ctx, finish := traceFn(context.Background(), msg)
	finish()

	forwarded := &Message{Data: []byte("forwarded")}
	_, closeSpan := newTestTracer().TracePublish(ctx, resource("projects/test-project/topics/next-topic"), forwarded)
	closeSpan("server-id", nil)

	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(), dataStreamsCarrier(forwarded.Attributes),
	))
	require.True(t, ok, "pathway not found in forwarded message attributes")

	wantCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	wantCtx, ok = tracer.SetDataStreamsCheckpoint(wantCtx, consumeEdgeTags(resource(dsmSubscriptionFull))...)
	require.True(t, ok)
	wantCtx, ok = tracer.SetDataStreamsCheckpoint(wantCtx, produceEdgeTags(resource("projects/test-project/topics/next-topic"))...)
	require.True(t, ok)
	want, _ := datastreams.PathwayFromContext(wantCtx)
	assert.NotZero(t, want.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func TestTraceReceiveFuncDataStreamsOverridesInheritedPathway(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	producerCtx, ok := tracer.SetDataStreamsCheckpoint(context.Background(), produceEdgeTags(resource(dsmTopicFull))...)
	require.True(t, ok)

	msg := &Message{ID: "1", Data: []byte("hello"), Attributes: map[string]string{}}
	datastreams.InjectToBase64Carrier(producerCtx, dataStreamsCarrier(msg.Attributes))

	inheritedCtx, ok := tracer.SetDataStreamsCheckpoint(
		context.Background(),
		consumeEdgeTags(resource("projects/test-project/subscriptions/other-subscription"))...,
	)
	require.True(t, ok)
	inherited, _ := datastreams.PathwayFromContext(inheritedCtx)

	traceFn := newTestTracer().TraceReceiveFunc(resource(dsmSubscriptionFull))
	ctx, finish := traceFn(inheritedCtx, msg)
	finish()

	_, ok = tracer.SpanFromContext(ctx)
	require.True(t, ok, "handler context must keep its span")

	wantCtx, ok := tracer.SetDataStreamsCheckpoint(producerCtx, consumeEdgeTags(resource(dsmSubscriptionFull))...)
	require.True(t, ok)
	want, _ := datastreams.PathwayFromContext(wantCtx)

	got, ok := datastreams.PathwayFromContext(ctx)
	require.True(t, ok, "pathway not found in receive handler context")
	assert.NotZero(t, want.GetHash())
	assert.NotEqual(t, inherited.GetHash(), got.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func TestEdgeResourceName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		collection string
		want       string
	}{
		{name: "topic path", input: "projects/commerce/topics/orders", collection: "topics", want: "orders"},
		{name: "subscription path", input: "projects/commerce/subscriptions/orders", collection: "subscriptions", want: "orders"},
		{name: "domain-scoped project", input: "projects/example.com:commerce/topics/orders", collection: "topics", want: "orders"},
		{name: "short name unchanged", input: "orders", collection: "topics", want: "orders"},
		{name: "wrong collection unchanged", input: "projects/commerce/subscriptions/orders", collection: "topics", want: "projects/commerce/subscriptions/orders"},
		{name: "missing resource unchanged", input: "projects/commerce/topics/", collection: "topics", want: "projects/commerce/topics/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, edgeResourceName(tc.input, tc.collection))
		})
	}
}

func TestProduceEdgeTags(t *testing.T) {
	assert.Equal(t,
		[]string{"direction:out", "topic:orders", "type:google-pubsub"},
		produceEdgeTags(resource("projects/commerce/topics/orders")),
	)
}

func TestConsumeEdgeTags(t *testing.T) {
	assert.Equal(t,
		[]string{"direction:in", "subscription:orders", "type:google-pubsub"},
		consumeEdgeTags(resource("projects/commerce/subscriptions/orders")),
	)
}
