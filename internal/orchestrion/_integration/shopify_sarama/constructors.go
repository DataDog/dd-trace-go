// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package shopify_sarama

import (
	"context"
	"testing"

	saramatrace "github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2"
	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/shopify_sarama/internal/kafkamock"
)

// The cases below count spans against an in-process broker, one message per
// producer or consumer. An extra span means a value was wrapped twice. sarama's
// sync producer constructors build an async producer and type assert it to
// sarama's own type, so wrapping that inner one panics.

type mockBroker struct {
	addrs []string
	cfg   *sarama.Config
}

func (b *mockBroker) Setup(_ context.Context, t *testing.T) {
	broker := kafkamock.Start(t)
	t.Cleanup(broker.Close)
	b.addrs = []string{broker.Addr()}
	b.cfg = kafkamock.Config()
}

func (b *mockBroker) newClient(t *testing.T) sarama.Client {
	t.Helper()
	client, err := sarama.NewClient(b.addrs, b.cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func produceSpan() *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "kafka.produce",
			"type":     "queue",
			"service":  "kafka",
			"resource": "Produce Topic " + kafkamock.Topic,
		},
		Meta: map[string]string{
			"span.kind": "producer",
			"component": "Shopify/sarama",
		},
	}
}

func consumeSpan() *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "kafka.consume",
			"type":     "queue",
			"service":  "kafka",
			"resource": "Consume Topic " + kafkamock.Topic,
		},
		Meta: map[string]string{
			"span.kind": "consumer",
			"component": "Shopify/sarama",
		},
	}
}

type TestCaseSyncProducer struct{ mockBroker }

func (tc *TestCaseSyncProducer) Run(_ context.Context, t *testing.T) {
	p, err := sarama.NewSyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(p))
}

func (*TestCaseSyncProducer) ExpectedTraces() trace.Traces { return trace.Traces{produceSpan()} }
func (*TestCaseSyncProducer) ExpectedSpanCount() int       { return 1 }

type TestCaseSyncProducerFromClient struct{ mockBroker }

func (tc *TestCaseSyncProducerFromClient) Run(_ context.Context, t *testing.T) {
	p, err := sarama.NewSyncProducerFromClient(tc.newClient(t))
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(p))
}

func (*TestCaseSyncProducerFromClient) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan()}
}
func (*TestCaseSyncProducerFromClient) ExpectedSpanCount() int { return 1 }

type TestCaseAsyncProducer struct{ mockBroker }

func (tc *TestCaseAsyncProducer) Run(_ context.Context, t *testing.T) {
	p, err := sarama.NewAsyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendAsync(p))
}

func (*TestCaseAsyncProducer) ExpectedTraces() trace.Traces { return trace.Traces{produceSpan()} }
func (*TestCaseAsyncProducer) ExpectedSpanCount() int       { return 1 }

type TestCaseAsyncProducerFromClient struct{ mockBroker }

func (tc *TestCaseAsyncProducerFromClient) Run(_ context.Context, t *testing.T) {
	p, err := sarama.NewAsyncProducerFromClient(tc.newClient(t))
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendAsync(p))
}

func (*TestCaseAsyncProducerFromClient) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan()}
}
func (*TestCaseAsyncProducerFromClient) ExpectedSpanCount() int { return 1 }

type TestCaseConsumer struct{ mockBroker }

func (tc *TestCaseConsumer) Run(_ context.Context, t *testing.T) {
	c, err := sarama.NewConsumer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(c))
}

func (*TestCaseConsumer) ExpectedTraces() trace.Traces { return trace.Traces{consumeSpan()} }
func (*TestCaseConsumer) ExpectedSpanCount() int       { return 1 }

type TestCaseConsumerFromClient struct{ mockBroker }

func (tc *TestCaseConsumerFromClient) Run(_ context.Context, t *testing.T) {
	c, err := sarama.NewConsumerFromClient(tc.newClient(t))
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(c))
}

func (*TestCaseConsumerFromClient) ExpectedTraces() trace.Traces {
	return trace.Traces{consumeSpan()}
}
func (*TestCaseConsumerFromClient) ExpectedSpanCount() int { return 1 }

// TestCaseWrappedTwice wraps values that automatic tracing already wrapped, and
// pins what orchestrion does.
//
// FIXME: the contrib cannot tell that a value is already wrapped, so the sync
// producer and the consumer trace each message twice. The async producer gives
// one span instead of two: the inner wrapper replaces the span context in the
// message headers, so the outer one never finds its span to finish it.
type TestCaseWrappedTwice struct{ mockBroker }

func (tc *TestCaseWrappedTwice) Run(_ context.Context, t *testing.T) {
	sp, err := sarama.NewSyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendSync(saramatrace.WrapSyncProducer(tc.cfg, sp)))

	ap, err := sarama.NewAsyncProducer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.SendAsync(saramatrace.WrapAsyncProducer(tc.cfg, ap)))

	c, err := sarama.NewConsumer(tc.addrs, tc.cfg)
	require.NoError(t, err)
	require.NoError(t, kafkamock.Consume(saramatrace.WrapConsumer(c)))
}

func (*TestCaseWrappedTwice) ExpectedTraces() trace.Traces {
	return trace.Traces{produceSpan(), consumeSpan()}
}
func (*TestCaseWrappedTwice) ExpectedSpanCount() int { return 5 }
