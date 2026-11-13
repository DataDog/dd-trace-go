// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/stretchr/testify/require"

	kafkatrace "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// The cases below run against librdkafka's in-process mock cluster, so the
// only spans are the ones kafka reports. Each case produces one message and
// consumes it, so an extra span means a client was traced twice.

const (
	mockTopic     = "confluent_kafka_v1_mock_test"
	mockComponent = "confluentinc/confluent-kafka-go/kafka"
)

type mockCluster struct {
	servers string
}

func (m *mockCluster) Setup(_ context.Context, t *testing.T) {
	cluster, err := kafka.NewMockCluster(1)
	require.NoError(t, err)
	t.Cleanup(cluster.Close)
	m.servers = cluster.BootstrapServers()
}

func (m *mockCluster) producerConfig() *kafka.ConfigMap {
	return &kafka.ConfigMap{"bootstrap.servers": m.servers}
}

func (m *mockCluster) consumerConfig(group string) *kafka.ConfigMap {
	return &kafka.ConfigMap{
		"bootstrap.servers":        m.servers,
		"group.id":                 group,
		"enable.auto.offset.store": false,
	}
}

// host is the value of the messaging.kafka.bootstrap.servers tag.
func (m *mockCluster) host() string {
	host, _, _ := net.SplitHostPort(strings.Split(m.servers, ",")[0])
	return host
}

func mockMessage(key string) *kafka.Message {
	topic := mockTopic
	return &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: 0},
		Key:            []byte(key),
		Value:          []byte("value"),
	}
}

func assign(t *testing.T, c interface {
	Assign([]kafka.TopicPartition) error
}) {
	t.Helper()
	topic := mockTopic
	require.NoError(t, c.Assign([]kafka.TopicPartition{{Topic: &topic, Partition: 0, Offset: kafka.OffsetBeginning}}))
}

// requireDelivered waits for the delivery report of one message.
func requireDelivered(t *testing.T, events <-chan kafka.Event) {
	t.Helper()
	for {
		select {
		case evt := <-events:
			if msg, ok := evt.(*kafka.Message); ok {
				require.NoError(t, msg.TopicPartition.Error)
				return
			}
		case <-time.After(10 * time.Second):
			require.FailNow(t, "no delivery report")
		}
	}
}

// pollMessage polls until it gets a message.
func pollMessage(t *testing.T, poll func(int) kafka.Event) *kafka.Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if msg, ok := poll(100).(*kafka.Message); ok {
			require.NoError(t, msg.TopicPartition.Error)
			return msg
		}
	}
	require.FailNow(t, "no message")
	return nil
}

// mockTrace is one produce span with its consume span as child. otelc passes
// the producer config to the contrib, so the produce span also has the
// bootstrap servers tag there.
func (m *mockCluster) mockTrace() *trace.Trace {
	produceMeta := map[string]string{
		"span.kind":        "producer",
		"component":        mockComponent,
		"messaging.system": "kafka",
	}
	if otelc.Enabled() {
		produceMeta["messaging.kafka.bootstrap.servers"] = m.host()
	}
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "kafka.produce",
			"type":     "queue",
			"service":  "kafka",
			"resource": "Produce Topic " + mockTopic,
		},
		Meta: produceMeta,
		Children: trace.Traces{
			{
				Tags: map[string]any{
					"name":     "kafka.consume",
					"type":     "queue",
					"service":  "kafka",
					"resource": "Consume Topic " + mockTopic,
				},
				Meta: map[string]string{
					"span.kind":                         "consumer",
					"component":                         mockComponent,
					"messaging.system":                  "kafka",
					"messaging.kafka.bootstrap.servers": m.host(),
				},
			},
		},
	}
}

// TestCaseProduceAndPoll covers Producer.Produce with a delivery channel, and
// Consumer.Poll followed by Consumer.Commit.
type TestCaseProduceAndPoll struct{ mockCluster }

func (tc *TestCaseProduceAndPoll) Run(_ context.Context, t *testing.T) {
	p, err := kafka.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("poll"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	c, err := kafka.NewConsumer(tc.consumerConfig("poll"))
	require.NoError(t, err)
	assign(t, c)
	msg := pollMessage(t, c.Poll)
	require.Equal(t, "poll", string(msg.Key))
	_, err = c.StoreMessage(msg)
	require.NoError(t, err)
	_, err = c.Commit()
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

func (tc *TestCaseProduceAndPoll) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCaseProduceAndPoll) ExpectedSpanCount() int { return 2 }

// TestCaseReadMessage covers Consumer.ReadMessage followed by
// Consumer.CommitMessage.
type TestCaseReadMessage struct{ mockCluster }

func (tc *TestCaseReadMessage) Run(_ context.Context, t *testing.T) {
	p, err := kafka.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("read-message"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	c, err := kafka.NewConsumer(tc.consumerConfig("read-message"))
	require.NoError(t, err)
	assign(t, c)
	msg, err := c.ReadMessage(10 * time.Second)
	require.NoError(t, err)
	require.Equal(t, "read-message", string(msg.Key))
	_, err = c.CommitMessage(msg)
	require.NoError(t, err)
	require.NoError(t, c.Close())
}

func (tc *TestCaseReadMessage) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCaseReadMessage) ExpectedSpanCount() int { return 2 }

// TestCaseChannels covers Producer.ProduceChannel, and a consumer reading
// Consumer.Events followed by Consumer.CommitOffsets.
type TestCaseChannels struct{ mockCluster }

func (tc *TestCaseChannels) Run(_ context.Context, t *testing.T) {
	p, err := kafka.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	p.ProduceChannel() <- mockMessage("channels")
	requireDelivered(t, p.Events())
	p.Close()

	cfg := tc.consumerConfig("channels")
	require.NoError(t, cfg.SetKey("go.events.channel.enable", true))
	c, err := kafka.NewConsumer(cfg)
	require.NoError(t, err)
	assign(t, c)
	var msg *kafka.Message
	for msg == nil {
		select {
		case evt := <-c.Events():
			msg, _ = evt.(*kafka.Message)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "no message")
		}
	}
	require.Equal(t, "channels", string(msg.Key))
	_, err = c.CommitOffsets([]kafka.TopicPartition{{
		Topic:     msg.TopicPartition.Topic,
		Partition: msg.TopicPartition.Partition,
		Offset:    msg.TopicPartition.Offset + 1,
	}})
	require.NoError(t, err)
	require.NoError(t, c.Close())
	// The consume span finishes when the traced events channel closes.
	for range c.Events() {
	}
}

func (tc *TestCaseChannels) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCaseChannels) ExpectedSpanCount() int { return 2 }

// TestCaseContribClients checks that clients built with the contrib are traced
// once. Orchestrion traces them twice, so it only runs under otelc.
type TestCaseContribClients struct{ mockCluster }

func (tc *TestCaseContribClients) Setup(ctx context.Context, t *testing.T) {
	if !otelc.Enabled() {
		t.Skip("orchestrion traces clients built with the contrib twice")
	}
	tc.mockCluster.Setup(ctx, t)
}

func (tc *TestCaseContribClients) Run(_ context.Context, t *testing.T) {
	p, err := kafkatrace.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("contrib"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	c, err := kafkatrace.NewConsumer(tc.consumerConfig("contrib"))
	require.NoError(t, err)
	assign(t, c)
	msg := pollMessage(t, c.Poll)
	require.Equal(t, "contrib", string(msg.Key))
	require.NoError(t, c.Close())
}

func (tc *TestCaseContribClients) ExpectedTraces() trace.Traces {
	// The contrib's NewProducer passes its config, under both tools.
	tr := tc.mockTrace()
	tr.Meta["messaging.kafka.bootstrap.servers"] = tc.host()
	return trace.Traces{tr}
}
func (*TestCaseContribClients) ExpectedSpanCount() int { return 2 }

// TestCaseWrappedTwice wraps clients that automatic tracing already traces,
// and pins what orchestrion does.
//
// FIXME: the hooks cannot tell that the application also wrapped a client
// with the contrib, so each message is traced by both, with one produce and
// one consume span each.
type TestCaseWrappedTwice struct{ mockCluster }

func (tc *TestCaseWrappedTwice) Run(_ context.Context, t *testing.T) {
	rawProducer, err := kafka.NewProducer(tc.producerConfig())
	require.NoError(t, err)
	p := kafkatrace.WrapProducer(rawProducer)
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, p.Produce(mockMessage("wrapped-twice"), delivery))
	requireDelivered(t, delivery)
	p.Close()

	rawConsumer, err := kafka.NewConsumer(tc.consumerConfig("wrapped-twice"))
	require.NoError(t, err)
	c := kafkatrace.WrapConsumer(rawConsumer)
	assign(t, c)
	msg := pollMessage(t, c.Poll)
	require.Equal(t, "wrapped-twice", string(msg.Key))
	require.NoError(t, c.Close())
}

func (*TestCaseWrappedTwice) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{"name": "kafka.produce"},
			Meta: map[string]string{"component": mockComponent},
		},
	}
}
func (*TestCaseWrappedTwice) ExpectedSpanCount() int { return 4 }
