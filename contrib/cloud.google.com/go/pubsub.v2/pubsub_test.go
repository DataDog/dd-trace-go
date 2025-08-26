// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func lowerEqual(t *testing.T, id uint64, tid [16]byte) {
	assert.Equal(t, id, binary.BigEndian.Uint64(tid[8:]))
}

func TestPropagation(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, pub, sub := setup(t)

	// Publisher
	span, pctx := tracer.StartSpanFromContext(ctx, "propagation-test", tracer.WithSpanID(42)) // set the root trace ID
	srvID, err := Publish(pctx, pub, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(pctx)
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
		lowerEqual(t, 42, span.Context().TraceIDBytes())
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
	assert.Subset(filterTags(spans[0].Tags()), map[string]any{
		"message_size":      float64(5),
		"num_attributes":    float64(5),
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/topics/topic",
		ext.SpanType:        ext.SpanTypeMessageProducer,
		"server_id":         srvID,
		ext.ServiceName:     "",
		ext.Component:       "cloud.google.com/go/pubsub.v2",
		ext.SpanKind:        ext.SpanKindProducer,
		ext.MessagingSystem: "googlepubsub",
		ext.SpanName:        "pubsub.publish",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v2", spans[0].Integration())

	assert.Equal(spans[0].SpanID(), spans[2].ParentID())
	assert.Equal(uint64(42), spans[2].TraceID())
	assert.Equal(spanID, spans[2].SpanID())
	assert.Subset(filterTags(spans[2].Tags()), map[string]any{
		"message_size":      float64(5),
		"num_attributes":    float64(5),
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v2",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.MessagingSystem: "googlepubsub",
		ext.ServiceName:     "",
		ext.SpanName:        "pubsub.receive",
	}, spans[2].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v2", spans[2].Integration())
}

func TestPropagationWithServiceName(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, pub, sub := setup(t)

	// Publisher
	span, pctx := tracer.StartSpanFromContext(ctx, "service-name-test")
	_, err := Publish(pctx, pub, &pubsub.Message{Data: []byte("hello")}).Get(pctx)
	assert.NoError(err)
	span.Finish()

	// Subscriber
	err = sub.Receive(ctx, WrapReceiveHandler(sub, func(_ context.Context, msg *pubsub.Message) {
		msg.Ack()
		cancel()
	}, WithService("example.service")))
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 3, "wrong number of spans")
	assert.Equal("example.service", spans[2].Tag(ext.ServiceName))
}

func TestPropagationNoParentSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, pub, sub := setup(t)

	// Publisher
	// no parent span
	srvID, err := Publish(ctx, pub, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(ctx)
	assert.NoError(err)

	// Subscriber
	var (
		msgID   string
		spanID  uint64
		traceID string
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
	assert.Equal(traceID, spans[0].Context().TraceID())
	assert.Subset(filterTags(spans[0].Tags()), map[string]any{
		"message_size":      float64(5),
		"num_attributes":    float64(5),
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/topics/topic",
		ext.SpanType:        ext.SpanTypeMessageProducer,
		"server_id":         srvID,
		ext.Component:       "cloud.google.com/go/pubsub.v2",
		ext.SpanKind:        ext.SpanKindProducer,
		ext.MessagingSystem: "googlepubsub",
		ext.ServiceName:     "",
		ext.SpanName:        "pubsub.publish",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v2", spans[0].Integration())

	assert.Equal(spans[0].SpanID(), spans[1].ParentID())
	assert.Equal(traceID, spans[1].Context().TraceID())
	assert.Equal(spanID, spans[1].SpanID())
	assert.Subset(filterTags(spans[1].Tags()), map[string]any{
		"message_size":      float64(5),
		"num_attributes":    float64(5),
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v2",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.MessagingSystem: "googlepubsub",
		ext.ServiceName:     "",
		ext.SpanName:        "pubsub.receive",
	}, spans[1].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v2", spans[1].Integration())
}

func TestPropagationNoPublisherSpan(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel, mt, pub, sub := setup(t)

	// Publisher
	// no tracing on publisher side
	_, err := pub.Publish(ctx, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(ctx)
	assert.NoError(err)

	// Subscriber
	var (
		msgID   string
		spanID  uint64
		traceID string
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

	assert.Equal(traceID, spans[0].Context().TraceID())
	assert.Equal(spanID, spans[0].SpanID())
	assert.Subset(filterTags(spans[0].Tags()), map[string]any{
		"message_size":      float64(5),
		"num_attributes":    float64(0), // no attributes, since no publish middleware sent them
		"ordering_key":      "xxx",
		ext.ResourceName:    "projects/project/subscriptions/subscription",
		ext.SpanType:        ext.SpanTypeMessageConsumer,
		"message_id":        msgID,
		"publish_time":      pubTime,
		ext.Component:       "cloud.google.com/go/pubsub.v2",
		ext.SpanKind:        ext.SpanKindConsumer,
		ext.MessagingSystem: "googlepubsub",
		ext.ServiceName:     "",
		ext.SpanName:        "pubsub.receive",
	}, spans[0].Tags())
	assert.Equal("cloud.google.com/go/pubsub.v2", spans[0].Integration())
}

func filterTags(m map[string]any) map[string]any {
	delete(m, "_dd.p.tid")
	delete(m, "_dd.profiling.enabled")
	delete(m, "_dd.top_level")
	delete(m, "_sampling_priority_v1")
	delete(m, "language")
	delete(m, "tracestate")
	return m
}

func setup(t *testing.T) (context.Context, context.CancelFunc, mocktracer.Tracer, *pubsub.Publisher, *pubsub.Subscriber) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)

	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	t.Cleanup(cancel)

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	const projectID = "project"
	client, err := pubsub.NewClient(ctx, projectID, option.WithGRPCConn(conn))
	require.NoError(t, err)

	topic, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: fmt.Sprintf("projects/%s/topics/topic", projectID),
	})
	require.NoError(t, err)

	publisher := client.Publisher(topic.Name)
	publisher.EnableMessageOrdering = true

	subscription, err := client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  fmt.Sprintf("projects/%s/subscriptions/subscription", projectID),
		Topic: topic.Name,
	})
	require.NoError(t, err)

	sub := client.Subscriber(subscription.Name)
	return ctx, cancel, mt, publisher, sub
}
