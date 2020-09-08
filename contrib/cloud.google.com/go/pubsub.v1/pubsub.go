package pubsub

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"cloud.google.com/go/pubsub"
)

// Publish publishes a message on the specified topic, waits for publishing to complete, and returns the result.
// It is functionally equivalent to (*pubsub.Topic).Publish(ctx, msg).Get(ctx), but it also ensures that the datadog
// tracing metadata is propagated as attributes attached to the published message.
func Publish(ctx context.Context, t *pubsub.Topic, msg *pubsub.Message) (serverID string, err error) {
	span, ctx := tracer.StartSpanFromContext(
		ctx,
		"pubsub.publish",
		tracer.ResourceName(t.String()),
		tracer.SpanType(ext.SpanTypeMessageProducer),
		tracer.Tag("message_size", len(msg.Data)),
		tracer.Tag("ordering_key", msg.OrderingKey),
	)
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	if msg.Attributes == nil {
		msg.Attributes = make(map[string]string)
	}
	tracer.Inject(span.Context(), tracer.TextMapCarrier(msg.Attributes))
	span.SetTag("num_attributes", len(msg.Attributes))
	serverID, err = t.Publish(ctx, msg).Get(ctx)
	span.SetTag("server_id", serverID)
	return
}

// ReceiveTracer returns a receive callback that wraps the supplied callback, and extracts the datadog tracing metadata
// if it exists attached to the received message.
func ReceiveTracer(s *pubsub.Subscription, f func(context.Context, *pubsub.Message)) func(context.Context, *pubsub.Message) {
	return func(ctx context.Context, msg *pubsub.Message) {
		parentSpanCtx, _ := tracer.Extract(tracer.TextMapCarrier(msg.Attributes))
		span, ctx := tracer.StartSpanFromContext(
			ctx,
			"pubsub.receive",
			tracer.ResourceName(s.String()),
			tracer.SpanType(ext.SpanTypeMessageConsumer),
			tracer.Tag("message_size", len(msg.Data)),
			tracer.Tag("num_attributes", len(msg.Attributes)),
			tracer.Tag("ordering_key", msg.OrderingKey),
			tracer.Tag("message_id", msg.ID),
			tracer.Tag("publish_time", msg.PublishTime.String()),
			tracer.ChildOf(parentSpanCtx),
		)
		if msg.DeliveryAttempt != nil {
			span.SetTag("delivery_attempt", *msg.DeliveryAttempt)
		}
		defer span.Finish()
		f(ctx, msg)
	}
}
