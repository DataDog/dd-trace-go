// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package segmentio_kafka_v0

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkatrace "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const componentName = "segmentio/kafka.go.v0"

// TestExactSpans checks the exact kafka spans of each reader and writer path.
func TestExactSpans(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartKafkaTestContainer(t, nil)
	var (
		topicBatch    = createTopic(t, addr, "exact_batch")
		topicNoParent = createTopic(t, addr, "exact_no_parent")
		topicErr      = createTopic(t, addr, "exact_err")
		topicRead     = createTopic(t, addr, "exact_read")
		topicReadErr  = createTopic(t, addr, "exact_read_err")
		topicContrib  = createTopic(t, addr, "exact_contrib")
	)

	t.Run("WriterBatch", func(t *testing.T) {
		spans := traced(t, func(ctx context.Context) {
			root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
			defer root.Finish()
			writer := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topicBatch}
			defer func() { require.NoError(t, writer.Close()) }()
			require.NoError(t, writer.WriteMessages(ctx, messages(3)...))
		})
		root := requireOne(t, spans, "test.root")
		produced := filter(spans, "kafka.produce")
		require.Len(t, produced, 3)
		require.Len(t, spans, 4)
		for _, s := range produced {
			assert.Equal(t, "Produce Topic "+topicBatch, s.Resource)
			assert.Equal(t, root.SpanID, s.ParentID, "produce spans are siblings under the root")
			assert.Equal(t, addr, s.Meta["messaging.kafka.bootstrap.servers"])
		}
	})

	t.Run("WriterNoParent", func(t *testing.T) {
		spans := traced(t, func(ctx context.Context) {
			writer := kafka.NewWriter(kafka.WriterConfig{Brokers: []string{addr}, Topic: topicNoParent})
			defer func() { require.NoError(t, writer.Close()) }()
			require.NoError(t, writer.WriteMessages(ctx, messages(2)...))
		})
		produced := filter(spans, "kafka.produce")
		require.Len(t, produced, 2)
		require.Len(t, spans, 2)
		for _, s := range produced {
			assert.Zero(t, s.ParentID, "with no parent, each produce span is a root")
		}
		assert.NotEqual(t, produced[0].TraceID, produced[1].TraceID)
	})

	t.Run("WriterError", func(t *testing.T) {
		spans := traced(t, func(ctx context.Context) {
			writer := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topicErr}
			require.NoError(t, writer.WriteMessages(ctx))
			require.NoError(t, writer.Close())
			require.ErrorIs(t, writer.WriteMessages(ctx, messages(1)...), io.ErrClosedPipe)
		})
		produced := filter(spans, "kafka.produce")
		require.Len(t, produced, 1, "a write of no messages has no span")
		require.Len(t, spans, 1)
		assert.EqualValues(t, 1, produced[0].Error)
	})

	t.Run("ReaderFetchBatch", func(t *testing.T) {
		var fetched []kafka.Message
		spans := traced(t, func(ctx context.Context) {
			root, ctx := tracer.StartSpanFromContext(ctx, "test.root")
			writer := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topicRead}
			require.NoError(t, writer.WriteMessages(ctx, messages(3)...))
			require.NoError(t, writer.Close())
			root.Finish()

			reader := newPartitionReader(addr, topicRead)
			for range 3 {
				msg, err := reader.FetchMessage(context.Background())
				require.NoError(t, err)
				fetched = append(fetched, msg)
			}
			// The last span stays open until Close.
			require.NoError(t, reader.Close())
		})
		produced := filter(spans, "kafka.produce")
		consumed := filter(spans, "kafka.consume")
		require.Len(t, produced, 3)
		require.Len(t, consumed, 3)
		require.Len(t, spans, 7)
		producedIDs := map[uint64]bool{}
		for _, s := range produced {
			producedIDs[s.SpanID] = true
		}
		consumedIDs := map[string]bool{}
		for _, s := range consumed {
			assert.Equal(t, "Consume Topic "+topicRead, s.Resource)
			assert.True(t, producedIDs[s.ParentID], "consume spans are children of produce spans")
			consumedIDs[strconv.FormatUint(s.SpanID, 10)] = true
		}
		// The consume span context is injected into the message returned to the
		// caller.
		for _, msg := range fetched {
			assert.True(t, consumedIDs[header(msg, "x-datadog-parent-id")], "message headers: %v", msg.Headers)
		}
	})

	t.Run("ReaderReadAndFetchError", func(t *testing.T) {
		spans := traced(t, func(ctx context.Context) {
			writer := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topicReadErr}
			require.NoError(t, writer.WriteMessages(ctx, messages(1)...))
			require.NoError(t, writer.Close())

			reader := newPartitionReader(addr, topicReadErr)
			_, err := reader.ReadMessage(ctx)
			require.NoError(t, err)
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			_, err = reader.FetchMessage(canceled)
			require.ErrorIs(t, err, context.Canceled)
			require.NoError(t, reader.Close())
		})
		require.Len(t, filter(spans, "kafka.produce"), 1)
		require.Len(t, filter(spans, "kafka.consume"), 1, "a failed fetch has no span")
		require.Len(t, spans, 2)
	})

	// Orchestrion weaves kafka-go and the contrib's own calls into it, so it
	// traces each message twice. The contrib marks those calls for otelc.
	t.Run("ContribWrapped", func(t *testing.T) {
		if !otelc.Enabled() {
			t.Skip("orchestrion reports two spans per message for a reader or writer wrapped with the contrib")
		}
		spans := traced(t, func(ctx context.Context) {
			writer := kafkatrace.WrapWriter(&kafka.Writer{Addr: kafka.TCP(addr), Topic: topicContrib})
			require.NoError(t, writer.WriteMessages(ctx, messages(2)...))
			require.NoError(t, writer.Close())

			reader := kafkatrace.NewReader(kafka.ReaderConfig{
				Brokers: []string{addr},
				Topic:   topicContrib,
				MaxWait: 10 * time.Millisecond,
			})
			_, err := reader.FetchMessage(ctx)
			require.NoError(t, err)
			_, err = reader.ReadMessage(ctx)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
		})
		require.Len(t, filter(spans, "kafka.produce"), 2)
		require.Len(t, filter(spans, "kafka.consume"), 2)
		require.Len(t, spans, 4)
	})
}

// traced runs fn with a fresh tracer and returns the kafka and test.root spans
// it reported. Spans of other components, such as the net/http spans of the
// Docker API calls testcontainers makes in the background, are dropped.
func traced(t *testing.T, fn func(context.Context)) []*agenttest.Span {
	t.Helper()
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)
	fn(context.Background())
	tr.Stop()
	lister, ok := agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	var spans []*agenttest.Span
	for _, s := range lister.Spans() {
		if s.Operation == "test.root" || s.Meta["component"] == componentName {
			spans = append(spans, s)
		}
	}
	return spans
}

func filter(spans []*agenttest.Span, operation string) []*agenttest.Span {
	var out []*agenttest.Span
	for _, s := range spans {
		if s.Operation == operation {
			out = append(out, s)
		}
	}
	return out
}

func requireOne(t *testing.T, spans []*agenttest.Span, operation string) *agenttest.Span {
	t.Helper()
	found := filter(spans, operation)
	require.Len(t, found, 1, "%s spans", operation)
	return found[0]
}

func messages(n int) []kafka.Message {
	msgs := make([]kafka.Message, n)
	for i := range msgs {
		msgs[i] = kafka.Message{Key: []byte("key"), Value: []byte("message " + strconv.Itoa(i))}
	}
	return msgs
}

func header(msg kafka.Message, key string) string {
	for _, h := range msg.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func newPartitionReader(addr, topic string) *kafka.Reader {
	return kafka.NewReader(kafka.ReaderConfig{
		Brokers:   []string{addr},
		Topic:     topic,
		Partition: 0,
		MaxWait:   10 * time.Millisecond,
	})
}

// createTopic creates a topic with a unique name, since the Kafka container
// may be reused with messages from an earlier run, and waits for its leader.
// The first write of a subtest then does not fail with UnknownTopicOrPartition
// and need a retry, which would add spans. kafka.Conn is not traced.
func createTopic(t *testing.T, addr, name string) string {
	t.Helper()
	topic := "segmentio_kafka_" + name + "_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	conn, err := kafka.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.CreateTopics(kafka.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}))
	require.Eventually(t, func() bool {
		leader, err := kafka.DialLeader(context.Background(), "tcp", addr, topic, 0)
		if err != nil {
			return false
		}
		_ = leader.Close()
		return true
	}, 30*time.Second, 100*time.Millisecond, "topic %s has no leader", topic)
	return topic
}
