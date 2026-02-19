// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	testGroupID = "kgo-test-group-id"
)

var (
	seedBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}
)

// topicName returns a unique topic name for the current test.
func topicName(t *testing.T) string {
	return strings.ReplaceAll("twmb_franz-go_"+t.Name(), "/", "_")
}

func TestMain(m *testing.M) {
	// _, ok := os.LookupEnv("INTEGRATION")
	// if !ok {
	// 	log.Println("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	// 	os.Exit(0)
	// }
	os.Exit(m.Run())
}

// createTopicWithCleanup creates a topic and registers cleanup with t.Cleanup.
func createTopicWithCleanup(t *testing.T, topic string) {
	t.Helper()

	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	require.NoError(t, err)

	admCl := kadm.NewClient(cl)
	ctx := context.Background()

	// Delete if exists, ignore errors
	_, _ = admCl.DeleteTopics(ctx, topic)

	_, err = admCl.CreateTopic(ctx, 1, 1, nil, topic)
	require.NoError(t, err)

	// Wait for topic to be ready
	err = ensureTopicReady(topic)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, err := admCl.DeleteTopics(context.Background(), topic)
		if err != nil {
			log.Printf("failed to delete topic %s: %v", topic, err)
		} else {
			log.Printf("deleted topic %s", topic)
		}
		admCl.Close()
		cl.Close()
	})
}

func ensureTopicReady(topic string) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	if err != nil {
		return err
	}
	defer cl.Close()

	admCl := kadm.NewClient(cl)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for {
		metadata, err := admCl.Metadata(ctx, topic)
		if err != nil {
			return err
		}

		topicMeta, ok := metadata.Topics[topic]
		if ok && len(topicMeta.Partitions) > 0 && topicMeta.Err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type producedRecords struct {
	records []*kgo.Record
}

func (r *producedRecords) OnProduceRecordUnbuffered(record *kgo.Record, err error) {
	r.records = append(r.records, record)
}

func TestProduceFunctional(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	var (
		recordsToProduce = []*kgo.Record{
			{
				Topic: topic,
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}
		producedRecords = &producedRecords{}
	)

	producerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		// Hook so we can capture the produced records
		kgo.WithHooks(producedRecords),
	))
	require.NoError(t, err)
	defer producerCl.Close()

	// Pinging to run OnBrokerConnect before the actual testing records
	err = producerCl.Ping(context.Background())
	require.NoError(t, err)

	err = producerCl.ProduceSync(context.Background(), recordsToProduce...).FirstErr()
	require.NoError(t, err)

	require.Len(t, producedRecords.records, len(recordsToProduce))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+topic, s0.Tag(ext.ResourceName))
	// assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "twmb/franz-go", s0.Tag(ext.Component))
	assert.Equal(t, "twmb/franz-go", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
	assert.Contains(t, "localhost:9092,localhost:9093,localhost:9094", s0.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, topic, s0.Tag("messaging.destination.name"))

	h0 := producedRecords.records[0].Headers
	h0map := make(map[string]string)
	for _, header := range h0 {
		h0map[header.Key] = string(header.Value)
	}
	assert.Equal(t, strconv.FormatUint(s0.Context().TraceIDLower(), 10), h0map["x-datadog-trace-id"])
	assert.Equal(t, strconv.FormatUint(s0.Context().SpanID(), 10), h0map["x-datadog-parent-id"])
	assert.Equal(t, "_dd.p.tid="+strconv.FormatUint(s0.Context().TraceIDUpper(), 16), h0map["x-datadog-tags"])
	assert.NotEmpty(t, h0map["traceparent"])
	assert.NotEmpty(t, h0map["tracestate"])
}

func TestProduceConsumeFunctional(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	var (
		recordsToProduce = []*kgo.Record{
			{
				Topic: topic,
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}
		producedRecords = &producedRecords{}
	)

	consumerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(testGroupID),
	))
	require.NoError(t, err)

	producerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		kgo.WithHooks(producedRecords),
	))
	require.NoError(t, err)
	defer producerCl.Close()

	err = producerCl.ProduceSync(context.Background(), recordsToProduce...).FirstErr()
	require.NoError(t, err)

	ctx := context.Background()

	fetches := consumerCl.PollFetches(ctx)
	require.NoError(t, fetches.Err())

	records := fetches.Records()
	require.Len(t, records, 1)
	assert.Equal(t, []byte("key1"), records[0].Key)
	assert.Equal(t, []byte("value1"), records[0].Value)

	consumerCl.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	s0 := spans[0]
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+topic, s0.Tag(ext.ResourceName))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, "twmb/franz-go", s0.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))

	s1 := spans[1]
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+topic, s1.Tag(ext.ResourceName))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s1.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "twmb/franz-go", s1.Tag(ext.Component))
	assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
	assert.Contains(t, "localhost:9092,localhost:9093,localhost:9094", s1.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, topic, s1.Tag("messaging.destination.name"))

	assert.Equal(t, s0.SpanID(), s1.ParentID(), "consume span should be child of the produce span")
	assert.Equal(t, s0.TraceID(), s1.TraceID(), "spans should have the same trace id")
}

func TestProduceErrorFunctional(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	producerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
	))
	require.NoError(t, err)
	defer producerCl.Close()

	err = producerCl.Ping(context.Background())
	require.NoError(t, err)

	// We force an error by producing to partition 99 which doesn't exist
	record := &kgo.Record{
		Topic:     topic,
		Key:       []byte("key1"),
		Value:     []byte("value1"),
		Partition: 99,
	}
	err = producerCl.ProduceSync(context.Background(), record).FirstErr()
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s := spans[0]
	assert.Equal(t, "kafka.produce", s.OperationName())
	assert.Contains(t, s.Tag(ext.ErrorMsg), "invalid record partitioning")
}

func TestConsumeSpansFinishedOnNextPoll(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	consumerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(testGroupID),
	))
	require.NoError(t, err)
	defer consumerCl.Close()

	producerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
	))
	require.NoError(t, err)
	defer producerCl.Close()

	// Produce first message
	err = producerCl.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic,
		Value: []byte("message1"),
	}).FirstErr()
	require.NoError(t, err)

	// First poll - creates consume span but doesn't finish it yet
	fetches := consumerCl.PollFetches(context.Background())
	require.NoError(t, fetches.Err())
	require.Len(t, fetches.Records(), 1)

	// At this point, consume span is still active (not finished)
	// Only the produce span should be finished
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "kafka.produce", spans[0].OperationName())

	// Produce second message
	err = producerCl.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic,
		Value: []byte("message2"),
	}).FirstErr()
	require.NoError(t, err)

	// Second poll - should finish previous consume span and create new one
	fetches = consumerCl.PollFetches(context.Background())
	require.NoError(t, fetches.Err())
	require.Len(t, fetches.Records(), 1)

	// Now we should have: produce1, produce2, consume1 (finished by second poll)
	spans = mt.FinishedSpans()
	require.Len(t, spans, 3)
	assert.Equal(t, "kafka.produce", spans[0].OperationName())
	assert.Equal(t, "kafka.produce", spans[1].OperationName())
	assert.Equal(t, "kafka.consume", spans[2].OperationName())
}

func TestConsumeSpansFinishedOnClose(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	consumerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(testGroupID),
	))
	require.NoError(t, err)

	producerCl, err := NewClient(ClientOptions(
		kgo.SeedBrokers(seedBrokers...),
	))
	require.NoError(t, err)
	defer producerCl.Close()

	err = producerCl.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic,
		Value: []byte("message1"),
	}).FirstErr()
	require.NoError(t, err)

	fetches := consumerCl.PollFetches(context.Background())
	require.NoError(t, fetches.Err())
	require.Len(t, fetches.Records(), 1)

	// Before close: only produce span is finished
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "kafka.produce", spans[0].OperationName())

	// Close should finish the active consume span
	consumerCl.Close()

	// After close: both produce and consume spans are finished
	spans = mt.FinishedSpans()
	require.Len(t, spans, 2)
	assert.Equal(t, "kafka.produce", spans[0].OperationName())
	assert.Equal(t, "kafka.consume", spans[1].OperationName())
}

func TestProduceDSMPathway(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	producedRecords := &producedRecords{}

	producerCl, err := NewClient(
		ClientOptions(
			kgo.SeedBrokers(seedBrokers...),
			kgo.WithHooks(producedRecords),
		),
		tracing.WithDataStreams(),
	)
	require.NoError(t, err)
	defer producerCl.Close()

	err = producerCl.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic,
		Value: []byte("message1"),
	}).FirstErr()
	require.NoError(t, err)

	require.Len(t, producedRecords.records, 1)
	record := producedRecords.records[0]

	// Extract pathway from record headers
	carrier := tracing.NewKafkaHeadersCarrier(wrapRecord(record))
	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(),
		carrier,
	))
	require.True(t, ok, "pathway not found in kafka message headers")

	// Create expected pathway
	ctx, _ := tracer.SetDataStreamsCheckpoint(
		context.Background(),
		"direction:out", "topic:"+topic, "type:kafka",
	)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, uint64(0), want.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func TestConsumeDSMPathway(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	producerCl, err := NewClient(
		ClientOptions(kgo.SeedBrokers(seedBrokers...)),
		tracing.WithDataStreams(),
	)
	require.NoError(t, err)
	defer producerCl.Close()

	consumerCl, err := NewClient(
		ClientOptions(
			kgo.SeedBrokers(seedBrokers...),
			kgo.ConsumeTopics(topic),
			kgo.ConsumerGroup(testGroupID),
		),
		tracing.WithDataStreams(),
	)
	require.NoError(t, err)
	defer consumerCl.Close()

	err = producerCl.ProduceSync(context.Background(), &kgo.Record{
		Topic: topic,
		Value: []byte("message1"),
	}).FirstErr()
	require.NoError(t, err)

	fetches := consumerCl.PollFetches(context.Background())
	require.NoError(t, fetches.Err())
	records := fetches.Records()
	require.Len(t, records, 1)

	// Get the actual group ID that franz-go reports (used for DSM checkpoint)
	actualGroupID, _ := consumerCl.Client.GroupMetadata()
	require.NotEmpty(t, actualGroupID, "consumer should have joined a group")

	record := records[0]

	// Extract pathway from consumed record headers
	carrier := tracing.NewKafkaHeadersCarrier(wrapRecord(record))
	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(),
		carrier,
	))
	require.True(t, ok, "pathway not found in kafka message headers")

	// Create expected pathway: produce checkpoint -> consume checkpoint
	// Use the actual group ID that franz-go reports (may differ from configured)
	ctx, _ := tracer.SetDataStreamsCheckpoint(
		context.Background(),
		"direction:out", "topic:"+topic, "type:kafka",
	)
	ctx, _ = tracer.SetDataStreamsCheckpoint(
		ctx,
		"direction:in", "topic:"+topic, "type:kafka", "group:"+actualGroupID,
	)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, uint64(0), want.GetHash())
	assert.Equal(t, want.GetHash(), got.GetHash())
}
