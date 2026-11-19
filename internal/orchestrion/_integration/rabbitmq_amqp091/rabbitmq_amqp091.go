// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

//go:build linux || !githubci

package rabbitmqamqp091

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

func openChannel(t *testing.T, queue string) *amqp.Channel {
	t.Helper()
	containers.SkipIfProviderIsNotHealthy(t)
	_, url := containers.StartRabbitMQTestContainer(t)
	conn, err := amqp.Dial(url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	ch, err := conn.Channel()
	require.NoError(t, err)
	_, err = ch.QueueDeclare(queue, false, true, false, false, nil)
	require.NoError(t, err)
	return ch
}

func expectedTraces(consumeResource string, queue string) trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "test.root"},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "amqp.publish",
						"type":     "queue",
						"resource": "basic.publish  -> " + queue,
					},
					Meta: map[string]string{
						"span.kind":        "producer",
						"component":        "rabbitmq/amqp091-go",
						"messaging.system": "rabbitmq",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "amqp.consume",
								"type":     "queue",
								"resource": consumeResource + " " + queue,
							},
							Meta: map[string]string{
								"span.kind":                  "consumer",
								"component":                  "rabbitmq/amqp091-go",
								"messaging.system":           "rabbitmq",
								"messaging.destination.name": queue,
							},
						},
					},
				},
			},
		},
	}
}

type TestCase struct {
	ch *amqp.Channel
}

const consumeQueue = "orchestrion-amqp091-consume"

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.ch = openChannel(t, consumeQueue)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	deliveries, err := tc.ch.Consume(consumeQueue, "orchestrion", true, false, false, false, nil)
	require.NoError(t, err)
	require.NoError(t, tc.ch.PublishWithContext(ctx, "", consumeQueue, false, false, amqp.Publishing{Body: []byte("hello")}))
	span.Finish()

	select {
	case d := <-deliveries:
		require.Equal(t, "hello", string(d.Body))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
	}
	require.NoError(t, tc.ch.Cancel("orchestrion", false))
	for range deliveries {
	}
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return expectedTraces("basic.deliver", consumeQueue)
}

type TestCaseGet struct {
	ch *amqp.Channel
}

const getQueue = "orchestrion-amqp091-get"

func (tc *TestCaseGet) Setup(_ context.Context, t *testing.T) {
	tc.ch = openChannel(t, getQueue)
}

func (tc *TestCaseGet) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	publish(ctx, t, tc.ch)

	require.Eventually(t, func() bool {
		_, ok, err := tc.ch.Get(getQueue, true)
		require.NoError(t, err)
		return ok
	}, 10*time.Second, 50*time.Millisecond)
}

func publish(ctx context.Context, t *testing.T, ch *amqp.Channel) {
	_ = ctx
	require.NoError(t, ch.Publish("", getQueue, false, false, amqp.Publishing{Body: []byte("hello")}))
}

func (*TestCaseGet) ExpectedTraces() trace.Traces {
	return expectedTraces("basic.get", getQueue)
}

type TestCaseWithContext struct {
	ch *amqp.Channel
}

const withContextQueue = "orchestrion-amqp091-with-context"

func (tc *TestCaseWithContext) Setup(_ context.Context, t *testing.T) {
	tc.ch = openChannel(t, withContextQueue)
}

func (tc *TestCaseWithContext) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	deliveries, err := tc.ch.ConsumeWithContext(ctx, withContextQueue, "orchestrion-ctx", true, false, false, false, nil)
	require.NoError(t, err)
	_, err = tc.ch.PublishWithDeferredConfirmWithContext(ctx, "", withContextQueue, false, false, amqp.Publishing{Body: []byte("hello")})
	require.NoError(t, err)
	span.Finish()

	select {
	case d := <-deliveries:
		require.Equal(t, "hello", string(d.Body))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
	}
	require.NoError(t, tc.ch.Cancel("orchestrion-ctx", false))
	for range deliveries {
	}
}

func (*TestCaseWithContext) ExpectedTraces() trace.Traces {
	return expectedTraces("basic.deliver", withContextQueue)
}
