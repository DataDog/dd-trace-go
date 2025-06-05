// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package pubsub

import (
	"context"
	"testing"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, topic, sub := setup(t)

	// Publisher
	span, pctx := tracer.StartSpanFromContext(ctx, "propagation-test", tracer.WithSpanID(42)) // set the root trace ID
	srvID, err := Publish(pctx, topic, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(pctx)
	assert.NoError(err)
	span.Finish()

	// Subscriber
	var (
		msgID   string
		spanID  uint64
		pubTime string
		called  bool
	)
	err = sub.Receive(ctx, WrapReceiveHandler(sub, func(ctx context.Context, msg *pubsub.Message) {
		assert.False(called, "callback called twice")
		assert.Equal(msg.Data, []byte("hello"), "wrong payload")
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(ok, "no span")
		assert.Equal(uint64(42), span.Context().TraceID(), "wrong trace id") // gist of the test: the trace ID must be the same as the root trace ID set above
		msgID = msg.ID
		spanID = span.Context().SpanID()
		pubTime = msg.PublishTime.String()
		msg.Ack()
		called = true
		cancel()
	}))
	assert.True(called, "callback not called")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 3, "wrong number of spans")
	assert.Equal("pubsub.publish", spans[0].OperationName())
	assert.Equal("propagation-test", spans[1].OperationName())
	assert.Equal("pubsub.receive", spans[2].OperationName())

	assert.Equal(spans[1].SpanID(), spans[0].ParentID())
	assert.Equal(uint64(42), spans[0].TraceID())
	assert.Subset(spans[0].Tags(), map[string]interface{}{
		"message_size":      5.,
		"num_attributes":    5., // 5  tracing attributes
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/topics/topic",
		ext.SpanType:        ext.SpanTypeMessageProducer,
		"server_id":         srvID,
		ext.ServiceName:     "",
		ext.Component:       "cloud.google.com/go/pubsub.v1",
		ext.SpanKind:        ext.SpanKindProducer,
		ext.SpanName:        "pubsub.publish",
		ext.MessagingSystem: "googlepubsub",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v1", spans[0].Integration())

	assert.Equal(spans[0].SpanID(), spans[2].ParentID())
	assert.Equal(uint64(42), spans[2].TraceID())
	assert.Equal(spanID, spans[2].SpanID())
	assert.Subset(spans[2].Tags(), map[string]interface{}{
		"message_size":      5.,
		"num_attributes":    5.,
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v1",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.SpanName:        "pubsub.receive",
		ext.MessagingSystem: "googlepubsub",
	}, spans[2].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v1", spans[2].Integration())
}

func TestPropagationWithServiceName(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, topic, sub := setup(t)

	// Publisher
	span, pctx := tracer.StartSpanFromContext(ctx, "service-name-test")
	_, err := Publish(pctx, topic, &pubsub.Message{Data: []byte("hello")}).Get(pctx)
	assert.NoError(err)
	span.Finish()

	// Subscriber
	err = sub.Receive(ctx, WrapReceiveHandler(sub, func(ctx context.Context, msg *pubsub.Message) {
		msg.Ack()
		cancel()
	}, WithServiceName("example.service")))
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 3, "wrong number of spans")
	assert.Equal("example.service", spans[2].Tag(ext.ServiceName))
}

func TestPropagationNoParentSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, topic, sub := setup(t)

	// Publisher
	// no parent span
	srvID, err := Publish(ctx, topic, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(ctx)
	assert.NoError(err)

	// Subscriber
	var (
		msgID   string
		spanID  uint64
		traceID uint64
		pubTime string
		called  bool
	)
	err = sub.Receive(ctx, WrapReceiveHandler(sub, func(ctx context.Context, msg *pubsub.Message) {
		assert.False(called, "callback called twice")
		assert.Equal(msg.Data, []byte("hello"), "wrong payload")
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(ok, "no span")
		msgID = msg.ID
		spanID = span.Context().SpanID()
		traceID = span.Context().TraceID()
		pubTime = msg.PublishTime.String()
		msg.Ack()
		called = true
		cancel()
	}))
	assert.True(called, "callback not called")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 2, "wrong number of spans")
	assert.Equal("pubsub.publish", spans[0].OperationName())
	assert.Equal("pubsub.receive", spans[1].OperationName())

	assert.Equal(spans[0].TraceID(), spans[0].SpanID())
	assert.Equal(traceID, spans[0].TraceID())
	assert.Subset(spans[0].Tags(), map[string]interface{}{
		"message_size":      5.,
		"num_attributes":    5.,
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/topics/topic",
		ext.SpanType:        ext.SpanTypeMessageProducer,
		"server_id":         srvID,
		ext.Component:       "cloud.google.com/go/pubsub.v1",
		ext.SpanKind:        ext.SpanKindProducer,
		ext.MessagingSystem: "googlepubsub",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v1", spans[0].Integration())

	assert.Equal(spans[0].SpanID(), spans[1].ParentID())
	assert.Equal(traceID, spans[1].TraceID())
	assert.Equal(spanID, spans[1].SpanID())
	assert.Subset(spans[1].Tags(), map[string]interface{}{
		"message_size":      5.,
		"num_attributes":    5.,
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v1",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.MessagingSystem: "googlepubsub",
	}, spans[1].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v1", spans[1].Integration())
}

func TestPropagationNoPublisherSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, topic, sub := setup(t)

	// Publisher
	// no tracing on publisher side
	_, err := topic.Publish(ctx, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(ctx)
	assert.NoError(err)

	// Subscriber
	var (
		msgID   string
		spanID  uint64
		traceID uint64
		pubTime string
		called  bool
	)
	err = sub.Receive(ctx, WrapReceiveHandler(sub, func(ctx context.Context, msg *pubsub.Message) {
		assert.False(called, "callback called twice")
		assert.Equal(msg.Data, []byte("hello"), "wrong payload")
		span, ok := tracer.SpanFromContext(ctx)
		assert.True(ok, "no span")
		msgID = msg.ID
		spanID = span.Context().SpanID()
		traceID = span.Context().TraceID()
		pubTime = msg.PublishTime.String()
		msg.Ack()
		called = true
		cancel()
	}))
	assert.True(called, "callback not called")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1, "wrong number of spans")
	assert.Equal("pubsub.receive", spans[0].OperationName())

	assert.Equal(traceID, spans[0].TraceID())
	assert.Equal(spanID, spans[0].SpanID())
	assert.Subset(spans[0].Tags(), map[string]interface{}{
		"message_size":      5.,
		"num_attributes":    0., // no attributes, since no publish middleware sent them
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v1",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.MessagingSystem: "googlepubsub",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v1", spans[0].Integration())
}

func setup(t *testing.T) (context.Context, context.CancelFunc, mocktracer.Tracer, *pubsub.Topic, *pubsub.Subscription) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)

	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	t.Cleanup(cancel)

	conn, err := grpc.Dial(srv.Addr, grpc.WithInsecure())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	require.NoError(t, err)

	_, err = client.CreateTopic(ctx, "topic")
	require.NoError(t, err)

	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true
	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	require.NoError(t, err)

	sub := client.Subscription("subscription")
	return ctx, cancel, mt, topic, sub
}
