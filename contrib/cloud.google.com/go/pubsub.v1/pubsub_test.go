// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pubsub

import (
	"context"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	ctx, topic, sub, mt, cleanup := setup(t)
	defer cleanup()

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
	err = sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
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
	assert.Equal(map[string]interface{}{
		"message_size":   5,
		"num_attributes": 2, // 2 tracing attributes
		"ordering_key":   "xxx",
		ext.ResourceName: "projects/project/topics/topic",
		ext.SpanType:     ext.SpanTypeMessageProducer,
		"server_id":      srvID,
		ext.ServiceName:  nil,
	}, spans[0].Tags())

	assert.Equal(spans[0].SpanID(), spans[2].ParentID())
	assert.Equal(uint64(42), spans[2].TraceID())
	assert.Equal(spanID, spans[2].SpanID())
	assert.Equal(map[string]interface{}{
		"message_size":   5,
		"num_attributes": 2,
		"ordering_key":   "xxx",
		ext.ResourceName: "projects/project/subscriptions/subscription",
		ext.SpanType:     ext.SpanTypeMessageConsumer,
		"message_id":     msgID,
		"publish_time":   pubTime,
	}, spans[2].Tags())
}

func TestPropagationNoParentSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, topic, sub, mt, cleanup := setup(t)
	defer cleanup()

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
	err = sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
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
	}))
	assert.True(called, "callback not called")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 2, "wrong number of spans")
	assert.Equal("pubsub.publish", spans[0].OperationName())
	assert.Equal("pubsub.receive", spans[1].OperationName())

	assert.Equal(spans[0].TraceID(), spans[0].SpanID())
	assert.Equal(traceID, spans[0].TraceID())
	assert.Equal(map[string]interface{}{
		"message_size":   5,
		"num_attributes": 2,
		"ordering_key":   "xxx",
		ext.ResourceName: "projects/project/topics/topic",
		ext.SpanType:     ext.SpanTypeMessageProducer,
		"server_id":      srvID,
	}, spans[0].Tags())

	assert.Equal(spans[0].SpanID(), spans[1].ParentID())
	assert.Equal(traceID, spans[1].TraceID())
	assert.Equal(spanID, spans[1].SpanID())
	assert.Equal(map[string]interface{}{
		"message_size":   5,
		"num_attributes": 2,
		"ordering_key":   "xxx",
		ext.ResourceName: "projects/project/subscriptions/subscription",
		ext.SpanType:     ext.SpanTypeMessageConsumer,
		"message_id":     msgID,
		"publish_time":   pubTime,
	}, spans[1].Tags())
}

func TestPropagationNoPubsliherSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, topic, sub, mt, cleanup := setup(t)
	defer cleanup()

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
	err = sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
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
	}))
	assert.True(called, "callback not called")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1, "wrong number of spans")
	assert.Equal("pubsub.receive", spans[0].OperationName())

	assert.Equal(traceID, spans[0].TraceID())
	assert.Equal(spanID, spans[0].SpanID())
	assert.Equal(map[string]interface{}{
		"message_size":   5,
		"num_attributes": 0, // no attributes, since no publish middleware sent them
		"ordering_key":   "xxx",
		ext.ResourceName: "projects/project/subscriptions/subscription",
		ext.SpanType:     ext.SpanTypeMessageConsumer,
		"message_id":     msgID,
		"publish_time":   pubTime,
	}, spans[0].Tags())
}

func setup(t *testing.T) (context.Context, *pubsub.Topic, *pubsub.Subscription, mocktracer.Tracer, func()) {
	assert := assert.New(t)
	mt := mocktracer.Start()

	srv := pstest.NewServer()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	conn, err := grpc.Dial(srv.Addr, grpc.WithInsecure())
	assert.NoError(err)
	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	assert.NoError(err)
	_, err = client.CreateTopic(ctx, "topic")
	assert.NoError(err)
	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true
	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	assert.NoError(err)
	sub := client.Subscription("subscription")

	return ctx, topic, sub, mt, func() {
		// use t.Cleanup() once go 1.14 is available
		conn.Close()
		cancel()
		srv.Close()
		mt.Stop()
	}
}
