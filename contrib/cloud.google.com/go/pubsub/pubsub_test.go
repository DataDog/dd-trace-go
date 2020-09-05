package pubsub

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestDatadogTracePropagation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := pstest.NewServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.Dial(srv.Addr, grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	assert.NoError(t, err)

	_, err = client.CreateTopic(ctx, "topic")
	assert.NoError(t, err)

	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true

	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	assert.NoError(t, err)

	sub := client.Subscription("subscription")

	// Publisher
	{
		span, pctx := tracer.StartSpanFromContext(ctx, "propagation-test", tracer.WithSpanID(42)) // set the root trace ID
		_, err := Publish(pctx, topic, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"})
		assert.NoError(t, err)
		span.Finish()
	}

	// Subscriber
	msgID := ""
	spanID := uint64(0)
	{
		called := false
		err := sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
			assert.False(t, called, "callback called twice")
			assert.Equal(t, msg.Data, []byte("hello"), "wrong payload")
			span, ok := tracer.SpanFromContext(ctx)
			assert.True(t, ok, "no span")
			assert.Equal(t, uint64(42), span.Context().TraceID(), "wrong trace id") // gist of the test: the trace ID must be the same as the root trace ID set above
			msgID = msg.ID
			spanID = span.Context().SpanID()
			msg.Ack()
			called = true
		}))
		assert.True(t, called, "callback not called")
		assert.NoError(t, err)
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 3, "wrong number of spans")
	assert.Equal(t, "pubsub.publish", spans[0].OperationName())
	assert.Equal(t, "propagation-test", spans[1].OperationName())
	assert.Equal(t, "pubsub.receive", spans[2].OperationName())

	assert.Equal(t, ext.SpanTypeMessageProducer, spans[0].Tag(ext.SpanType))
	assert.Equal(t, "projects/project/topics/topic", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, spans[1].SpanID(), spans[0].ParentID())
	assert.Equal(t, uint64(42), spans[0].TraceID())
	assert.Equal(t, 5, spans[0].Tag("message_size"))
	assert.Equal(t, 2, spans[0].Tag("num_attributes")) // 2 tracing attributes
	assert.Equal(t, "xxx", spans[0].Tag("ordering_key"))
	assert.Empty(t, spans[0].Tag("message_id"))
	assert.Empty(t, spans[0].Tag("publish_time"))
	assert.Empty(t, spans[0].Tag(ext.Error))

	assert.Equal(t, ext.SpanTypeMessageConsumer, spans[2].Tag(ext.SpanType))
	assert.Equal(t, "projects/project/subscriptions/subscription", spans[2].Tag(ext.ResourceName))
	assert.Equal(t, spans[0].SpanID(), spans[2].ParentID())
	assert.Equal(t, uint64(42), spans[2].TraceID())
	assert.Equal(t, spanID, spans[2].SpanID())
	assert.Equal(t, 5, spans[2].Tag("message_size"))
	assert.Equal(t, 2, spans[2].Tag("num_attributes"))
	assert.Equal(t, "xxx", spans[2].Tag("ordering_key"))
	assert.NotEmpty(t, spans[2].Tag("message_id"))
	assert.Equal(t, msgID, spans[2].Tag("message_id"))
	assert.NotEmpty(t, spans[2].Tag("publish_time"))
	assert.Empty(t, spans[2].Tag(ext.Error))
}

func TestDatadogTracePropagationNoParentSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := pstest.NewServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.Dial(srv.Addr, grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	assert.NoError(t, err)

	_, err = client.CreateTopic(ctx, "topic")
	assert.NoError(t, err)

	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true

	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	assert.NoError(t, err)

	sub := client.Subscription("subscription")

	// Publisher
	{
		// no parent span
		_, err := Publish(ctx, topic, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"})
		assert.NoError(t, err)
	}

	// Subscriber
	msgID := ""
	spanID := uint64(0)
	traceID := uint64(0)
	{
		called := false
		err := sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
			assert.False(t, called, "callback called twice")
			assert.Equal(t, msg.Data, []byte("hello"), "wrong payload")
			span, ok := tracer.SpanFromContext(ctx)
			assert.True(t, ok, "no span")
			msgID = msg.ID
			spanID = span.Context().SpanID()
			traceID = span.Context().TraceID()
			msg.Ack()
			called = true
		}))
		assert.True(t, called, "callback not called")
		assert.NoError(t, err)
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2, "wrong number of spans")
	assert.Equal(t, "pubsub.publish", spans[0].OperationName())
	assert.Equal(t, "pubsub.receive", spans[1].OperationName())

	assert.Equal(t, ext.SpanTypeMessageProducer, spans[0].Tag(ext.SpanType))
	assert.Equal(t, "projects/project/topics/topic", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, spans[0].TraceID(), spans[0].SpanID())
	assert.Equal(t, traceID, spans[0].TraceID())
	assert.Equal(t, 5, spans[0].Tag("message_size"))
	assert.Equal(t, 2, spans[0].Tag("num_attributes"))
	assert.Equal(t, "xxx", spans[0].Tag("ordering_key"))
	assert.Empty(t, spans[0].Tag("message_id"))
	assert.Empty(t, spans[0].Tag("publish_time"))
	assert.Empty(t, spans[0].Tag(ext.Error))

	assert.Equal(t, ext.SpanTypeMessageConsumer, spans[1].Tag(ext.SpanType))
	assert.Equal(t, "projects/project/subscriptions/subscription", spans[1].Tag(ext.ResourceName))
	assert.Equal(t, spans[0].SpanID(), spans[1].ParentID())
	assert.Equal(t, traceID, spans[1].TraceID())
	assert.Equal(t, spanID, spans[1].SpanID())
	assert.Equal(t, 5, spans[1].Tag("message_size"))
	assert.Equal(t, 2, spans[1].Tag("num_attributes"))
	assert.Equal(t, "xxx", spans[1].Tag("ordering_key"))
	assert.NotEmpty(t, spans[1].Tag("message_id"))
	assert.Equal(t, msgID, spans[1].Tag("message_id"))
	assert.NotEmpty(t, spans[1].Tag("publish_time"))
	assert.Empty(t, spans[1].Tag(ext.Error))
}

func TestDatadogTracePropagationNoPubsliherSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := pstest.NewServer()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.Dial(srv.Addr, grpc.WithInsecure())
	assert.NoError(t, err)
	defer conn.Close()

	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	assert.NoError(t, err)

	_, err = client.CreateTopic(ctx, "topic")
	assert.NoError(t, err)

	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true

	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	assert.NoError(t, err)

	sub := client.Subscription("subscription")

	// Publisher
	{
		// no tracing on publisher side
		_, err := topic.Publish(ctx, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}).Get(ctx)
		assert.NoError(t, err)
	}

	// Subscriber
	msgID := ""
	spanID := uint64(0)
	traceID := uint64(0)
	{
		called := false
		err := sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
			assert.False(t, called, "callback called twice")
			assert.Equal(t, msg.Data, []byte("hello"), "wrong payload")
			span, ok := tracer.SpanFromContext(ctx)
			assert.True(t, ok, "no span")
			msgID = msg.ID
			spanID = span.Context().SpanID()
			traceID = span.Context().TraceID()
			msg.Ack()
			called = true
		}))
		assert.True(t, called, "callback not called")
		assert.NoError(t, err)
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1, "wrong number of spans")
	assert.Equal(t, "pubsub.receive", spans[0].OperationName())

	assert.Equal(t, ext.SpanTypeMessageConsumer, spans[0].Tag(ext.SpanType))
	assert.Equal(t, "projects/project/subscriptions/subscription", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, traceID, spans[0].TraceID())
	assert.Equal(t, spanID, spans[0].SpanID())
	assert.Equal(t, 5, spans[0].Tag("message_size"))
	assert.Equal(t, 0, spans[0].Tag("num_attributes")) // no tracing attributes
	assert.Equal(t, "xxx", spans[0].Tag("ordering_key"))
	assert.NotEmpty(t, spans[0].Tag("message_id"))
	assert.Equal(t, msgID, spans[0].Tag("message_id"))
	assert.NotEmpty(t, spans[0].Tag("publish_time"))
	assert.Empty(t, spans[0].Tag(ext.Error))
}
