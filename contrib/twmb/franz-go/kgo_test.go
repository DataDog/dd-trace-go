// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	testGroupID       = "kgo-test-group-id"
	testTopic         = "kgo-test-topic"
	testReaderMaxWait = 10 * time.Millisecond
)

var (
	// Add dummy values to broker/addr to test bootstrap servers
	seedBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}
)

// NOTE: TestMain is executed first before the tests
// Do the setup, checks if you actually need to run the integration tests
func TestMain(m *testing.M) {
	// _, ok := os.LookupEnv("INTEGRATION")
	// if !ok {
	// 	log.Println("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	// 	os.Exit(0)
	// }
	cleanup := createTopic()
	exitCode := m.Run()
	cleanup()
	os.Exit(exitCode)
}

func createTopic() func() {
	// One client can both produce and consume!
	// Consuming can either be direct (no consumer group), or through a group. Below, we use a group.
	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	admCl := kadm.NewClient(cl)

	ctx := context.Background()
	_, err = admCl.DeleteTopics(ctx, testTopic)
	if err != nil && !errors.Is(err, kerr.UnknownTopicOrPartition) {
		log.Fatalf("failed to delete topic: %v", err)
	}

	_, err = admCl.CreateTopic(ctx, 1, 1, nil, testTopic)
	if err != nil {
		log.Fatalf("failed to create topic: %v", err)
	}

	if err := ensureTopicReady(); err != nil {
		log.Fatalf("failed to ensure topic is ready: %v", err)
	}

	return func() {
		defer admCl.Close()
		defer cl.Close()

		_, err = admCl.DeleteTopics(context.Background(), testTopic)
		if err != nil {
			log.Printf("failed to delete topic during cleanup: %v", err)
		}
	}
}

func ensureTopicReady() error {
	const (
		maxRetries = 10
		retryDelay = 100 * time.Millisecond
	)

	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	if err != nil {
		return err
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var retryCount int
	for retryCount < maxRetries {
		// Try to produce a test message
		record := &kgo.Record{
			Topic: testTopic,
			Key:   []byte("test-key"),
			Value: []byte("test-value"),
		}

		results := cl.ProduceSync(ctx, record)
		err = results.FirstErr()
		if err == nil {
			break
		}

		// This error happens sometimes with brand-new topics, as there is a delay between when the topic is created
		// on the broker, and when the topic can actually be written to.
		if errors.Is(err, kerr.UnknownTopicOrPartition) {
			retryCount++
			log.Printf("topic not ready yet, retrying produce in %s (retryCount: %d)\n", retryDelay, retryCount)
			time.Sleep(retryDelay)
			continue
		}
		return err
	}
	if err != nil {
		return err
	}

	// Consume the test message to clean up
	cl.AddConsumeTopics(testTopic)
	fetches := cl.PollFetches(ctx)
	if fetches.IsClientClosed() {
		return errors.New("client closed while polling")
	}

	return fetches.Err()
}

func testClient(t *testing.T, kgoOpts []kgo.Opt, tracingOpts ...tracing.Option) *Client {
	cl, err := NewClient(kgoOpts, tracingOpts...)
	require.NoError(t, err)
	return cl
}

type producedRecords struct {
	records []*kgo.Record
}

func (r *producedRecords) OnProduceRecordUnbuffered(record *kgo.Record, err error) {
	r.records = append(r.records, record)
}

// func generateSpans(t *testing.T, mt mocktracer.Tracer, producerOp func(t *testing.T, cl *Client), consumerOp func(t *testing.T, cl *Client), producerOpts []tracing.Option, consumerOpts []tracing.Option) ([]*mocktracer.Span, []*kgo.Record) {

// 	producerCl, err := NewClient(ClientOptions(
// 		kgo.SeedBrokers(seedBrokers...),
// 		kgo.ConsumeTopics(testTopic),
// 		kgo.ConsumerGroup(testGroupID),
// 		kgo.WithHooks(producedRecords),
// 	), producerOpts...)
// 	require.NoError(t, err)
// 	producerOp(t, producerCl)
// 	producerCl.Close()

// 	consumerCl, err := NewClient(ClientOptions(
// 		kgo.SeedBrokers(seedBrokers...),
// 		kgo.ConsumeTopics(testTopic),
// 		kgo.ConsumerGroup(testGroupID),
// 	), consumerOpts...)
// 	require.NoError(t, err)
// 	consumerOp(t, consumerCl)
// 	consumerCl.Close()

// 	spans := mt.FinishedSpans()
// 	require.Len(t, spans, 2)
// 	return spans, producedRecords.records
// }

func TestProduceConsumeFunctional(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var (
		recordsToProduce = []*kgo.Record{
			{
				Topic: testTopic,
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

	// TODO: Pinging to run OnBrokerConnect before the actual testing records
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
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	// assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
	assert.Equal(t, "twmb/franz-go", s0.Tag(ext.Component))
	assert.Equal(t, "twmb/franz-go", s0.Integration())
	assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
	assert.Contains(t, "localhost:9092,localhost:9093,localhost:9094", s0.Tag(ext.KafkaBootstrapServers))
	assert.Equal(t, testTopic, s0.Tag("messaging.destination.name"))

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
