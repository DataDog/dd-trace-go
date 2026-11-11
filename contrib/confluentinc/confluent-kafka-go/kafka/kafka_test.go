// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var (
	testGroupID = "gotest"
	testTopic   = "gotest"
)

func TestConsumerChannel(t *testing.T) {
	// we can test consuming via the Events channel by artifically sending
	// messages. Testing .Poll is done via an integration test.

	mt := mocktracer.Start()
	defer mt.Stop()

	c, err := NewConsumer(&kafka.ConfigMap{
		"go.events.channel.enable": true, // required for the events channel to be turned on
		"group.id":                 testGroupID,
		"socket.timeout.ms":        10,
		"session.timeout.ms":       10,
		"enable.auto.offset.store": false,
	}, WithAnalyticsRate(0.3), WithDataStreams())
	assert.NoError(t, err)

	err = c.Subscribe(testTopic, nil)
	assert.NoError(t, err)

	go func() {
		c.Consumer.Events() <- &kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &testTopic,
				Partition: 1,
				Offset:    1,
			},
			Key:   []byte("key1"),
			Value: []byte("value1"),
		}
		c.Consumer.Events() <- &kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &testTopic,
				Partition: 1,
				Offset:    2,
			},
			Key:   []byte("key2"),
			Value: []byte("value2"),
		}
	}()

	msg1 := (<-c.Events()).(*kafka.Message)
	assert.Equal(t, []byte("key1"), msg1.Key)
	msg2 := (<-c.Events()).(*kafka.Message)
	assert.Equal(t, []byte("key2"), msg2.Key)

	c.Close()
	// wait for the events channel to be closed
	<-c.Events()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	for i, s := range spans {
		assert.Equal(t, "kafka.consume", s.OperationName())
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "Consume Topic gotest", s.Tag(ext.ResourceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, float64(1), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, 0.3, s.Tag(ext.EventSampleRate))
		assert.EqualValues(t, kafka.Offset(i+1), s.Tag("offset"))
		assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s.Tag(ext.Component))
		assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s.Integration())
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, "gotest", s.Tag("messaging.destination.name"))
	}
	for _, msg := range []*kafka.Message{msg1, msg2} {
		p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), NewMessageCarrier(msg)))
		assert.True(t, ok)
		expectedCtx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "group:"+testGroupID, "direction:in", "topic:"+testTopic, "type:kafka")
		expected, _ := datastreams.PathwayFromContext(expectedCtx)
		assert.NotEqual(t, expected.GetHash(), 0)
		assert.Equal(t, expected.GetHash(), p.GetHash())
	}
}

func TestConsumerFunctional(t *testing.T) {
	for _, tt := range []struct {
		name   string
		action consumerActionFn
	}{
		{
			name: "Poll",
			action: func(c *Consumer) (*kafka.Message, error) {
				switch e := c.Poll(3000).(type) {
				case *kafka.Message:
					return e, nil
				default:
					return nil, errors.New("some error")
				}
			},
		},
		{
			name: "ReadMessage",
			action: func(c *Consumer) (*kafka.Message, error) {
				return c.ReadMessage(3000 * time.Millisecond)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spans, msg := produceThenConsume(t, tt.action, []Option{WithAnalyticsRate(0.1), WithDataStreams()}, []Option{WithDataStreams()})

			s0 := spans[0] // produce
			assert.Equal(t, "kafka.produce", s0.OperationName())
			assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
			assert.Equal(t, "Produce Topic gotest", s0.Tag(ext.ResourceName))
			assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
			assert.Equal(t, "queue", s0.Tag(ext.SpanType))
			assert.Equal(t, float64(0), s0.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s0.Tag(ext.Component))
			assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s0.Integration())
			assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
			assert.Equal(t, "127.0.0.1", s0.Tag(ext.KafkaBootstrapServers))
			assert.Equal(t, "gotest", s0.Tag("messaging.destination.name"))

			s1 := spans[1] // consume
			assert.Equal(t, "kafka.consume", s1.OperationName())
			assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
			assert.Equal(t, "Consume Topic gotest", s1.Tag(ext.ResourceName))
			assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
			assert.Equal(t, "queue", s1.Tag(ext.SpanType))
			assert.Equal(t, float64(0), s1.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s1.Tag(ext.Component))
			assert.Equal(t, "confluentinc/confluent-kafka-go/kafka", s1.Integration())
			assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
			assert.Equal(t, "127.0.0.1", s1.Tag(ext.KafkaBootstrapServers))
			assert.Equal(t, "gotest", s1.Tag("messaging.destination.name"))

			clusterID, ok := s0.Tag(ext.MessagingKafkaClusterID).(string)
			require.True(t, ok, "produce span should have a cluster ID tag")
			require.NotEmpty(t, clusterID)

			p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), NewMessageCarrier(msg)))
			assert.True(t, ok)
			mt := mocktracer.Start()
			ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka", "kafka_cluster_id:"+clusterID)
			expectedCtx, _ := tracer.SetDataStreamsCheckpoint(ctx, "group:"+testGroupID, "direction:in", "topic:"+testTopic, "type:kafka", "kafka_cluster_id:"+clusterID)
			expected, _ := datastreams.PathwayFromContext(expectedCtx)
			mt.Stop()
			assert.NotEqual(t, expected.GetHash(), 0)
			assert.Equal(t, expected.GetHash(), p.GetHash())
		})
	}
}

func TestConsumerFunctionalWithClusterID(t *testing.T) {
	action := func(c *Consumer) (*kafka.Message, error) {
		return c.ReadMessage(3000 * time.Millisecond)
	}
	spans, msg := produceThenConsume(t, action,
		[]Option{WithDataStreams()},
		[]Option{WithDataStreams()},
	)
	require.Len(t, spans, 2)

	// Verify cluster ID is set as a span tag on both produce and consume spans.
	// The cluster ID is auto-fetched from the broker, so we just verify it's
	// present and consistent across spans.
	s0 := spans[0] // produce
	s1 := spans[1] // consume
	clusterID, ok := s0.Tag(ext.MessagingKafkaClusterID).(string)
	require.True(t, ok, "produce span should have a cluster ID tag")
	assert.NotEmpty(t, clusterID)
	assert.Equal(t, clusterID, s1.Tag(ext.MessagingKafkaClusterID))

	// Verify DSM pathway hash includes kafka_cluster_id in edge tags
	p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), NewMessageCarrier(msg)))
	assert.True(t, ok)
	mt := mocktracer.Start()
	ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka", "kafka_cluster_id:"+clusterID)
	expectedCtx, _ := tracer.SetDataStreamsCheckpoint(ctx, "group:"+testGroupID, "direction:in", "topic:"+testTopic, "type:kafka", "kafka_cluster_id:"+clusterID)
	expected, _ := datastreams.PathwayFromContext(expectedCtx)
	mt.Stop()
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())
}

// TestConsumerFunctionalWithClusterIDWithoutDataStreams checks that, when
// data streams monitoring is disabled, the cluster ID is fetched on the first
// produce and consume, and tagged on the following spans.
func TestConsumerFunctionalWithClusterIDWithoutDataStreams(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	p, err := NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":   "127.0.0.1:9092",
		"go.delivery.reports": true,
	})
	require.NoError(t, err)
	require.False(t, p.tracer.DSMEnabled())
	require.False(t, p.clusterID.Started(), "the cluster ID must not be fetched before the first produce")

	produced := make([]*kafka.Message, 0, 2)
	for i := range 2 {
		delivery := make(chan kafka.Event, 1)
		err = p.Produce(&kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &testTopic, Partition: 0},
			Value:          []byte(fmt.Sprintf("value%d", i)),
		}, delivery)
		require.NoError(t, err)
		msg, _ := (<-delivery).(*kafka.Message)
		require.NoError(t, msg.TopicPartition.Error)
		produced = append(produced, msg)
		// The first produce starts the fetch.
		require.Eventually(t, func() bool { return p.tracer.ClusterID() != "" }, 5*time.Second, 10*time.Millisecond)
	}
	p.Close()

	c, err := NewConsumer(&kafka.ConfigMap{
		"group.id":                 testGroupID,
		"bootstrap.servers":        "127.0.0.1:9092",
		"fetch.wait.max.ms":        500,
		"socket.timeout.ms":        1500,
		"session.timeout.ms":       1500,
		"enable.auto.offset.store": false,
	})
	require.NoError(t, err)
	require.False(t, c.clusterID.Started(), "the cluster ID must not be fetched before the first consume")
	err = c.Assign([]kafka.TopicPartition{
		{Topic: &testTopic, Partition: 0, Offset: produced[0].TopicPartition.Offset},
	})
	require.NoError(t, err)
	for range 2 {
		_, err := c.ReadMessage(3000 * time.Millisecond)
		require.NoError(t, err)
		// The first consume starts the fetch.
		require.Eventually(t, func() bool { return c.tracer.ClusterID() != "" }, 5*time.Second, 10*time.Millisecond)
	}
	require.NoError(t, c.Close())

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)
	clusterID := p.tracer.ClusterID()
	assert.Equal(t, clusterID, c.tracer.ClusterID())
	for _, s := range spans {
		if s.Tag(ext.MessagingKafkaClusterID) != nil {
			assert.Equal(t, clusterID, s.Tag(ext.MessagingKafkaClusterID))
		}
	}
	// The second produce and the second consume happen after the fetch.
	var tagged int
	for _, s := range spans {
		if s.Tag(ext.MessagingKafkaClusterID) == clusterID {
			tagged++
		}
	}
	assert.GreaterOrEqual(t, tagged, 2)
}

// TestClusterIDFetchStartsOnFirstUse checks that, when data streams
// monitoring is disabled, the cluster ID fetch starts on the first produce or
// consume, and that a client closed without being used does
// not start it nor block in Close. It needs no broker.
func TestClusterIDFetchStartsOnFirstUse(t *testing.T) {
	newProducer := func(t *testing.T, opts ...Option) *Producer {
		p, err := NewProducer(&kafka.ConfigMap{
			"bootstrap.servers": "127.0.0.1:9092",
		}, opts...)
		require.NoError(t, err)
		return p
	}
	newConsumer := func(t *testing.T, eventsChannel bool, opts ...Option) *Consumer {
		c, err := NewConsumer(&kafka.ConfigMap{
			"go.events.channel.enable": eventsChannel, // lets the test inject messages
			"group.id":                 testGroupID,
			"bootstrap.servers":        "127.0.0.1:9092",
		}, opts...)
		require.NoError(t, err)
		return c
	}
	// drain reads the events left in ch until it is closed, so the wrapping
	// goroutines do not block on a send after the test.
	drain := func(ch chan kafka.Event) {
		go func() {
			for range ch {
			}
		}()
	}

	t.Run("producer closed without use", func(t *testing.T) {
		p := newProducer(t)
		require.False(t, p.tracer.DSMEnabled())
		assert.False(t, p.clusterID.Started())
		// The events channel is only wrapped for data streams monitoring.
		assert.Equal(t, p.Producer.Events(), p.Events())

		start := time.Now()
		p.Close()
		assert.Less(t, time.Since(start), time.Second, "Close must not wait for a cluster ID fetch")
		assert.False(t, p.clusterID.Started())
	})
	t.Run("consumer closed without use", func(t *testing.T) {
		c := newConsumer(t, false)
		require.False(t, c.tracer.DSMEnabled())
		assert.False(t, c.clusterID.Started())

		start := time.Now()
		require.NoError(t, c.Close())
		assert.Less(t, time.Since(start), time.Second, "Close must not wait for a cluster ID fetch")
		assert.False(t, c.clusterID.Started())
	})
	t.Run("producer starts on first produce", func(t *testing.T) {
		p := newProducer(t)
		defer p.Close()

		// An empty topic makes Produce fail right away, so no delivery report
		// is left pending. The fetch starts anyway.
		topic := ""
		msg := &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &topic}}
		require.Error(t, p.Produce(msg, nil))
		assert.True(t, p.clusterID.Started())
		// Starting at most once is covered by the kafkatrace tests.
		require.Error(t, p.Produce(msg, nil))
	})
	t.Run("producer starts on first message on the produce channel", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		p := newProducer(t)
		defer p.Close()

		p.ProduceChannel() <- &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &testTopic, Partition: 0}}
		assert.Eventually(t, p.clusterID.Started, time.Second, 10*time.Millisecond)
		// The goroutine that forwards the message to the producer finishes the
		// produce span after the send. Wait for it, so Close does not close
		// the producer's channel while the goroutine still sends on it.
		require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, time.Second, 10*time.Millisecond)
	})
	t.Run("consumer starts on first message", func(t *testing.T) {
		c := newConsumer(t, true)
		defer c.Close()

		c.Consumer.Events() <- &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: &testTopic, Partition: 0}}
		<-c.Events()
		drain(c.Events())
		assert.True(t, c.clusterID.Started())
	})
	t.Run("data streams starts on wrap", func(t *testing.T) {
		p := newProducer(t, WithDataStreams())
		defer p.Close()
		drain(p.Events())
		c := newConsumer(t, false, WithDataStreams())
		defer c.Close()

		assert.True(t, p.clusterID.Started())
		assert.True(t, c.clusterID.Started())
	})
}

// This tests the deprecated behavior of using cfg.context as the context passed via kafka messages
// instead of the one passed in the message.
func TestDeprecatedContext(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}

	tracer.Start()
	defer tracer.Stop()

	// Create the span to be passed
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "test_parent_context")

	c, err := NewConsumer(&kafka.ConfigMap{
		"go.events.channel.enable": true, // required for the events channel to be turned on
		"group.id":                 testGroupID,
		"socket.timeout.ms":        10,
		"session.timeout.ms":       10,
		"enable.auto.offset.store": false,
	}, WithContext(ctx)) // Adds the parent context containing a span
	assert.NoError(t, err)

	err = c.Subscribe(testTopic, nil)
	assert.NoError(t, err)

	// This span context will be ignored
	messageSpan, _ := tracer.StartSpanFromContext(context.Background(), "test_context_from_message")
	messageSpanContext := messageSpan.Context()

	/// Produce a message with a span
	go func() {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &testTopic,
				Partition: 1,
				Offset:    1,
			},
			Key:   []byte("key1"),
			Value: []byte("value1"),
		}

		// Inject the span context in the message to be produced
		carrier := NewMessageCarrier(msg)
		tracer.Inject(messageSpan.Context(), carrier)

		c.Consumer.Events() <- msg

	}()

	msg := (<-c.Events()).(*kafka.Message)

	// Extract the context from the message
	carrier := NewMessageCarrier(msg)
	spanContext, err := tracer.Extract(carrier)
	assert.NoError(t, err)

	parentContext := parentSpan.Context()

	/// The context passed is the one from the parent context
	assert.EqualValues(t, parentContext.TraceID(), spanContext.TraceID())
	/// The context passed is not the one passed in the message
	assert.NotEqualValues(t, messageSpanContext.TraceID(), spanContext.TraceID())

	c.Close()
	// wait for the events channel to be closed
	<-c.Events()

}

func TestCustomTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	c, err := NewConsumer(&kafka.ConfigMap{
		"go.events.channel.enable": true, // required for the events channel to be turned on
		"group.id":                 testGroupID,
		"socket.timeout.ms":        10,
		"session.timeout.ms":       10,
		"enable.auto.offset.store": false,
	}, WithCustomTag("foo", func(_ *kafka.Message) interface{} {
		return "bar"
	}), WithCustomTag("key", func(msg *kafka.Message) interface{} {
		return msg.Key
	}))
	assert.NoError(t, err)

	err = c.Subscribe(testTopic, nil)
	assert.NoError(t, err)

	go func() {
		c.Consumer.Events() <- &kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &testTopic,
				Partition: 1,
				Offset:    1,
			},
			Key:   []byte("key1"),
			Value: []byte("value1"),
		}
	}()

	<-c.Events()

	c.Close()
	// wait for the events channel to be closed
	<-c.Events()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	s := spans[0]

	assert.Equal(t, "bar", s.Tag("foo"))
	assert.Equal(t, "key1", s.Tag("key"))
}

// Test we don't leak goroutines and properly close the span when Produce returns an error
func TestProduceError(t *testing.T) {
	defer func() {
		err := goleak.Find()
		if err != nil {
			// if a goroutine is leaking, ensure it is not coming from this package
			if strings.Contains(err.Error(), "contrib/confluentinc/confluent-kafka-go") {
				assert.NoError(t, err, "found leaked goroutine(s) from this package")
			}
		}
	}()

	mt := mocktracer.Start()
	defer mt.Stop()

	// first write a message to the topic
	p, err := NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":   "127.0.0.1:9092",
		"go.delivery.reports": true,
	})
	require.NoError(t, err)
	defer p.Close()

	topic := ""
	msg := &kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic: &topic,
		},
	}
	deliveryChan := make(chan kafka.Event, 1)
	err = p.Produce(msg, deliveryChan)
	require.Error(t, err)
	require.EqualError(t, err, "Local: Invalid argument or configuration")

	select {
	case <-deliveryChan:
		assert.Fail(t, "there should be no events in the deliveryChan")
	case <-time.After(1 * time.Second):
		// assume there is no event
	}

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
}

type consumerActionFn func(c *Consumer) (*kafka.Message, error)

func produceThenConsume(t *testing.T, consumerAction consumerActionFn, producerOpts []Option, consumerOpts []Option) ([]*mocktracer.Span, *kafka.Message) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	// first write a message to the topic
	p, err := NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":   "127.0.0.1:9092",
		"go.delivery.reports": true,
	}, producerOpts...)
	require.NoError(t, err)
	if p.tracer.DSMEnabled() {
		require.Eventually(t, func() bool { return p.tracer.ClusterID() != "" }, 5*time.Second, 10*time.Millisecond)
	}

	delivery := make(chan kafka.Event, 1)
	err = p.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &testTopic,
			Partition: 0,
		},
		Key:   []byte("key2"),
		Value: []byte("value2"),
	}, delivery)
	require.NoError(t, err)

	msg1, _ := (<-delivery).(*kafka.Message)
	p.Close()

	// next attempt to consume the message
	c, err := NewConsumer(&kafka.ConfigMap{
		"group.id":                 testGroupID,
		"bootstrap.servers":        "127.0.0.1:9092",
		"fetch.wait.max.ms":        500,
		"socket.timeout.ms":        1500,
		"session.timeout.ms":       1500,
		"enable.auto.offset.store": false,
	}, consumerOpts...)
	require.NoError(t, err)
	if c.tracer.DSMEnabled() {
		require.Eventually(t, func() bool { return c.tracer.ClusterID() != "" }, 5*time.Second, 10*time.Millisecond)
	}

	err = c.Assign([]kafka.TopicPartition{
		{Topic: &testTopic, Partition: 0, Offset: msg1.TopicPartition.Offset},
	})
	require.NoError(t, err)

	msg2, err := consumerAction(c)
	require.NoError(t, err)
	_, err = c.CommitMessage(msg2)
	require.NoError(t, err)
	assert.Equal(t, msg1.String(), msg2.String())
	err = c.Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	if c.tracer.DSMEnabled() {
		backlogs := mt.SentDSMBacklogs()
		toMap := func(_ []mocktracer.DSMBacklog) map[string]struct{} {
			m := make(map[string]struct{})
			for _, b := range backlogs {
				m[strings.Join(b.Tags, "")] = struct{}{}
			}
			return m
		}
		backlogsMap := toMap(backlogs)
		clusterID := c.tracer.ClusterID()
		require.NotEmpty(t, clusterID)
		clusterTag := "kafka_cluster_id:" + clusterID
		require.Contains(t, backlogsMap, "consumer_group:"+testGroupID+"partition:0"+"topic:"+testTopic+"type:kafka_commit"+clusterTag)
		require.Contains(t, backlogsMap, "partition:0"+"topic:"+testTopic+"type:kafka_high_watermark"+clusterTag)
		require.Contains(t, backlogsMap, "partition:0"+"topic:"+testTopic+"type:kafka_produce"+clusterTag)
	}
	return spans, msg2
}

/*
to run the integration test locally:

    docker network create confluent

    docker run --rm \
        --name zookeeper \
        --network confluent \
        -p 2181:2181 \
        -e ZOOKEEPER_CLIENT_PORT=2181 \
        confluentinc/cp-zookeeper:5.0.0

    docker run --rm \
        --name kafka \
        --network confluent \
        -p 9092:9092 \
        -e KAFKA_ZOOKEEPER_CONNECT=zookeeper:2181 \
        -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:9092 \
        -e KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:9092 \
        -e KAFKA_CREATE_TOPICS=gotest:1:1 \
        -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
        confluentinc/cp-kafka:5.0.0
*/
