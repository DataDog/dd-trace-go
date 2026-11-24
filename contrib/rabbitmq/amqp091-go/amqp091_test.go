// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package amqp091

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func openChannel(t *testing.T) *amqp.Channel {
	t.Helper()
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	containers.SkipIfProviderIsNotHealthy(t)
	_, url := containers.StartRabbitMQTestContainer(t)
	conn, err := amqp.Dial(url)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	ch, err := conn.Channel()
	require.NoError(t, err)
	return ch
}

func declareQueue(t *testing.T, ch *amqp.Channel, exchange string) string {
	t.Helper()
	q, err := ch.QueueDeclare(strings.ReplaceAll(t.Name(), "/", "_"), false, true, false, false, nil)
	require.NoError(t, err)
	if exchange != "" {
		require.NoError(t, ch.ExchangeDeclare(exchange, "direct", false, true, false, false, nil))
		require.NoError(t, ch.QueueBind(q.Name, q.Name, exchange, false, nil))
	}
	return q.Name
}

func receive(t *testing.T, deliveries <-chan amqp.Delivery) amqp.Delivery {
	t.Helper()
	select {
	case d := <-deliveries:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
		return amqp.Delivery{}
	}
}

func waitForSpans(t *testing.T, mt mocktracer.Tracer, n int) []*mocktracer.Span {
	t.Helper()
	require.Eventually(t, func() bool { return len(mt.FinishedSpans()) >= n }, 10*time.Second, 50*time.Millisecond)
	return mt.FinishedSpans()
}

func expectedHash(t *testing.T, edges ...[]string) uint64 {
	t.Helper()
	ctx := context.Background()
	for _, e := range edges {
		ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, e...)
	}
	p, ok := datastreams.PathwayFromContext(ctx)
	require.True(t, ok)
	require.NotZero(t, p.GetHash())
	return p.GetHash()
}

func pathwayHash(t *testing.T, headers amqp.Table) uint64 {
	t.Helper()
	p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), TableCarrier(headers)))
	require.True(t, ok, "no pathway in headers %v", headers)
	return p.GetHash()
}

func TestPublishConsume(t *testing.T) {
	ch := openChannel(t)
	for _, tt := range []struct {
		name        string
		exchange    string
		produceEdge func(queue string) []string
	}{
		{
			name:        "default exchange",
			produceEdge: func(queue string) []string { return []string{"direction:out", "topic:" + queue, "type:rabbitmq"} },
		},
		{
			name:     "named exchange",
			exchange: "dd-amqp091-exchange",
			produceEdge: func(string) []string {
				return []string{"direction:out", "exchange:dd-amqp091-exchange", "has_routing_key:true", "type:rabbitmq"}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			queue := declareQueue(t, ch, tt.exchange)

			deliveries, err := Consume(ch, queue, t.Name(), true, false, false, false, nil, WithDataStreams())
			require.NoError(t, err)

			root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
			err = PublishWithContext(ctx, ch, tt.exchange, queue, false, false, amqp.Publishing{Body: []byte("hello"), Headers: amqp.Table{"user": "header"}}, WithDataStreams())
			require.NoError(t, err)
			root.Finish()

			d := receive(t, deliveries)
			assert.Equal(t, "hello", string(d.Body))
			assert.Equal(t, "header", d.Headers["user"])
			assert.Equal(t, expectedHash(t, tt.produceEdge(queue), []string{"direction:in", "topic:" + queue, "type:rabbitmq"}), pathwayHash(t, d.Headers))

			spans := waitForSpans(t, mt, 3)
			byName := map[string]*mocktracer.Span{}
			for _, s := range spans {
				byName[s.OperationName()] = s
			}
			publish, consume := byName["amqp.publish"], byName["amqp.consume"]
			require.NotNil(t, publish)
			require.NotNil(t, consume)

			assert.Equal(t, root.Context().SpanID(), publish.ParentID())
			assert.Equal(t, ext.SpanKindProducer, publish.Tag(ext.SpanKind))
			assert.Equal(t, string(instrumentation.PackageRabbitMQAMQP091Go), publish.Tag(ext.Component))
			assert.Equal(t, "rabbitmq", publish.Tag(ext.MessagingSystem))
			destination := tt.exchange
			if destination == "" {
				destination = "amq.default"
			}
			assert.Equal(t, destination, publish.Tag(ext.MessagingDestinationName))
			assert.Equal(t, queue, publish.Tag(routingKeyTag))
			assert.Equal(t, "basic.publish "+destination+" -> "+queue, publish.Tag(ext.ResourceName))

			assert.Equal(t, publish.SpanID(), consume.ParentID())
			assert.Equal(t, publish.TraceID(), consume.TraceID())
			assert.Equal(t, ext.SpanKindConsumer, consume.Tag(ext.SpanKind))
			assert.Equal(t, string(instrumentation.PackageRabbitMQAMQP091Go), consume.Tag(ext.Component))
			assert.Equal(t, queue, consume.Tag(ext.MessagingDestinationName))
			assert.Equal(t, "basic.deliver "+queue, consume.Tag(ext.ResourceName))
		})
	}
}

func TestPublishVariants(t *testing.T) {
	ch := openChannel(t)
	for _, tt := range []struct {
		name    string
		publish func(ch *amqp.Channel, queue string, msg amqp.Publishing) error
	}{
		{"Publish", func(ch *amqp.Channel, queue string, msg amqp.Publishing) error {
			return Publish(ch, "", queue, false, false, msg, WithDataStreams())
		}},
		{"PublishWithDeferredConfirm", func(ch *amqp.Channel, queue string, msg amqp.Publishing) error {
			_, err := PublishWithDeferredConfirm(ch, "", queue, false, false, msg, WithDataStreams())
			return err
		}},
		{"PublishWithDeferredConfirmWithContext", func(ch *amqp.Channel, queue string, msg amqp.Publishing) error {
			_, err := PublishWithDeferredConfirmWithContext(context.Background(), ch, "", queue, false, false, msg, WithDataStreams())
			return err
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			queue := declareQueue(t, ch, "")
			shared := amqp.Table{"user": "header"}

			require.NoError(t, tt.publish(ch, queue, amqp.Publishing{Body: []byte("hello"), Headers: shared}))
			assert.Equal(t, amqp.Table{"user": "header"}, shared)

			var d amqp.Delivery
			require.Eventually(t, func() bool {
				var ok bool
				var err error
				d, ok, err = Get(ch, queue, true, WithDataStreams())
				require.NoError(t, err)
				return ok
			}, 10*time.Second, 50*time.Millisecond)

			assert.Equal(t, expectedHash(t,
				[]string{"direction:out", "topic:" + queue, "type:rabbitmq"},
				[]string{"direction:in", "topic:" + queue, "type:rabbitmq"},
			), pathwayHash(t, d.Headers))

			spans := waitForSpans(t, mt, 2)
			assert.Equal(t, "amqp.publish", spans[0].OperationName())
			assert.Equal(t, "amqp.consume", spans[1].OperationName())
			assert.Equal(t, "basic.get "+queue, spans[1].Tag(ext.ResourceName))
			assert.Equal(t, spans[0].SpanID(), spans[1].ParentID())
		})
	}
}

func TestForward(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	first := declareQueue(t, ch, "")
	second, err := ch.QueueDeclare(first+"_forwarded", false, true, false, false, nil)
	require.NoError(t, err)

	deliveries, err := ConsumeWithContext(context.Background(), ch, first, "forwarder", true, false, false, false, nil, WithDataStreams())
	require.NoError(t, err)
	require.NoError(t, Publish(ch, "", first, false, false, amqp.Publishing{Body: []byte("hello")}, WithDataStreams()))

	d := receive(t, deliveries)
	consume := waitForSpans(t, mt, 2)[1]
	require.Equal(t, "amqp.consume", consume.OperationName())
	require.NoError(t, PublishWithContext(ContextFromDelivery(context.Background(), d), ch, "", second.Name, false, false, amqp.Publishing{Body: d.Body}, WithDataStreams()))

	var got amqp.Delivery
	require.Eventually(t, func() bool {
		var ok bool
		got, ok, err = Get(ch, second.Name, true, WithDataStreams())
		require.NoError(t, err)
		return ok
	}, 10*time.Second, 50*time.Millisecond)

	assert.Equal(t, expectedHash(t,
		[]string{"direction:out", "topic:" + first, "type:rabbitmq"},
		[]string{"direction:in", "topic:" + first, "type:rabbitmq"},
		[]string{"direction:out", "topic:" + second.Name, "type:rabbitmq"},
		[]string{"direction:in", "topic:" + second.Name, "type:rabbitmq"},
	), pathwayHash(t, got.Headers))

	forwarded := waitForSpans(t, mt, 4)[2]
	assert.Equal(t, "amqp.publish", forwarded.OperationName())
	assert.Equal(t, consume.SpanID(), forwarded.ParentID())
	assert.Equal(t, consume.TraceID(), forwarded.TraceID())
}

func TestDataStreamsDisabled(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	queue := declareQueue(t, ch, "")

	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("hello")}, WithService("svc")))
	var d amqp.Delivery
	require.Eventually(t, func() bool {
		var ok bool
		var err error
		d, ok, err = Get(ch, queue, true, WithService("svc"))
		require.NoError(t, err)
		return ok
	}, 10*time.Second, 50*time.Millisecond)

	assert.NotContains(t, d.Headers, "dd-pathway-ctx-base64")
	spans := waitForSpans(t, mt, 2)
	for _, s := range spans {
		assert.Equal(t, "svc", s.Tag(ext.ServiceName))
		assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, s.Tag(ext.KeyServiceSource))
	}
}

func TestPublishError(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	require.NoError(t, ch.Close())

	err := Publish(ch, "", "closed", false, false, amqp.Publishing{Body: []byte("hello")})
	require.Error(t, err)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.NotNil(t, spans[0].Tag(ext.ErrorMsg))
}

func TestOptions(t *testing.T) {
	cfg := buildConfig([]Option{WithService("svc"), WithCustomTag("team", "dsm"), WithDataStreams()})
	assert.Equal(t, "svc", cfg.serviceName)
	assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, cfg.serviceSource)
	assert.True(t, cfg.dataStreamsEnabled)
	assert.Equal(t, "dsm", cfg.producerBase.Tags["team"])
	assert.Equal(t, "dsm", cfg.consumerBase.Tags["team"])
	assert.Equal(t, ext.SpanKindProducer, cfg.producerBase.Tags[ext.SpanKind])
	assert.Equal(t, ext.SpanKindConsumer, cfg.consumerBase.Tags[ext.SpanKind])
	assert.Same(t, defaultConfig(), newConfig(nil))
}

func TestEmptyServiceName(t *testing.T) {
	cfg := &config{}
	base := cfg.baseSpanConfig(ext.SpanKindProducer, ext.SpanTypeMessageProducer)
	assert.NotContains(t, base.Tags, ext.ServiceName)
	assert.NotContains(t, base.Tags, ext.KeyServiceSource)
}

func TestTableCarrier(t *testing.T) {
	c := TableCarrier(amqp.Table{"bytes": []byte("b"), "number": int32(1)})
	c.Set("string", "s")

	v, ok := c.Get("bytes")
	assert.True(t, ok)
	assert.Equal(t, "b", v)
	_, ok = c.Get("number")
	assert.False(t, ok)

	seen := map[string]string{}
	require.NoError(t, c.ForeachKey(func(k, v string) error {
		seen[k] = v
		return nil
	}))
	assert.Equal(t, map[string]string{"bytes": "b", "string": "s"}, seen)

	assert.ErrorIs(t, c.ForeachKey(func(string, string) error { return assert.AnError }), assert.AnError)
}

func TestContextFromDeliveryWithoutHeaders(t *testing.T) {
	ctx := ContextFromDelivery(context.Background(), amqp.Delivery{})
	_, ok := datastreams.PathwayFromContext(ctx)
	assert.False(t, ok)
}

func TestConsumeWithContextCancel(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	queue := declareQueue(t, ch, "")

	ctx, cancel := context.WithCancel(context.Background())
	deliveries, err := ConsumeWithContext(ctx, ch, queue, "cancelled", true, false, false, false, nil)
	require.NoError(t, err)
	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("one")}))
	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("two")}))
	assert.Equal(t, "one", string(receive(t, deliveries).Body))
	cancel()

	require.Eventually(t, func() bool {
		select {
		case _, ok := <-deliveries:
			return !ok
		default:
			return false
		}
	}, 10*time.Second, 50*time.Millisecond)
}

func TestConsumeWithContextCancelPreservesDelivery(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	queue := declareQueue(t, ch, "")

	ctx, cancel := context.WithCancel(context.Background())
	deliveries, err := ConsumeWithContext(ctx, ch, queue, t.Name(), true, false, false, false, nil)
	require.NoError(t, err)
	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("hello")}))
	waitForOpenConsumeSpan(t, mt)

	cancel()

	select {
	case d, ok := <-deliveries:
		require.True(t, ok)
		assert.Equal(t, "hello", string(d.Body))
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery held by the relay was dropped when the context was canceled")
	}
}

func TestConsumeRelayExitsOnChannelClose(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	queue := declareQueue(t, ch, "")

	deliveries, err := Consume(ch, queue, t.Name(), true, false, false, false, nil)
	require.NoError(t, err)
	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("hello")}))
	waitForOpenConsumeSpan(t, mt)

	require.NoError(t, ch.Close())

	require.Eventually(t, func() bool {
		for _, s := range mt.FinishedSpans() {
			if s.OperationName() == "amqp.consume" {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "the relay leaked with an unfinished consume span after the channel closed")
	select {
	case _, ok := <-deliveries:
		assert.False(t, ok)
	case <-time.After(10 * time.Second):
		t.Fatal("the relay never closed the delivery channel after the channel closed")
	}
}

func waitForOpenConsumeSpan(t *testing.T, mt mocktracer.Tracer) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, s := range mt.OpenSpans() {
			if s.OperationName() == "amqp.consume" {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "no delivery reached the relay")
}

func TestConsumeSpanFinishesOnHandoff(t *testing.T) {
	ch := openChannel(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	queue := declareQueue(t, ch, "")

	deliveries, err := Consume(ch, queue, "handoff", true, false, false, false, nil)
	require.NoError(t, err)
	require.NoError(t, Publish(ch, "", queue, false, false, amqp.Publishing{Body: []byte("hello")}))
	receive(t, deliveries)

	spans := waitForSpans(t, mt, 2)
	assert.Equal(t, "amqp.consume", spans[1].OperationName())
}
