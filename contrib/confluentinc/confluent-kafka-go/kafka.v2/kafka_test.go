// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	internaldsm "gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testGroupID = "gotest"
	testTopic   = "gotest"
)

type consumerActionFn func(c *Consumer) (*kafka.Message, error)

func produceThenConsume(t *testing.T, consumerAction consumerActionFn, producerOpts []Option, consumerOpts []Option) ([]mocktracer.Span, *kafka.Message) {
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

	if c.cfg.dataStreamsEnabled {
		backlogs := mt.SentDSMBacklogs()
		toMap := func(b []internaldsm.Backlog) map[string]struct{} {
			m := make(map[string]struct{})
			for _, b := range backlogs {
				m[strings.Join(b.Tags, "")] = struct{}{}
			}
			return m
		}
		backlogsMap := toMap(backlogs)
		require.Contains(t, backlogsMap, "consumer_group:"+testGroupID+"partition:0"+"topic:"+testTopic+"type:kafka_commit")
		require.Contains(t, backlogsMap, "partition:0"+"topic:"+testTopic+"type:kafka_high_watermark")
		require.Contains(t, backlogsMap, "partition:0"+"topic:"+testTopic+"type:kafka_produce")
	}
	return spans, msg2
}

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
		assert.Equal(t, int32(1), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, 0.3, s.Tag(ext.EventSampleRate))
		assert.Equal(t, kafka.Offset(i+1), s.Tag("offset"))
		assert.Equal(t, componentName, s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindConsumer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
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
			assert.Equal(t, int32(0), s0.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, componentName, s0.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s0.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s0.Tag(ext.MessagingSystem))
			assert.Equal(t, "127.0.0.1", s0.Tag(ext.KafkaBootstrapServers))

			s1 := spans[1] // consume
			assert.Equal(t, "kafka.consume", s1.OperationName())
			assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
			assert.Equal(t, "Consume Topic gotest", s1.Tag(ext.ResourceName))
			assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
			assert.Equal(t, "queue", s1.Tag(ext.SpanType))
			assert.Equal(t, int32(0), s1.Tag(ext.MessagingKafkaPartition))
			assert.Equal(t, componentName, s1.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindConsumer, s1.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s1.Tag(ext.MessagingSystem))
			assert.Equal(t, "127.0.0.1", s1.Tag(ext.KafkaBootstrapServers))

			p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), NewMessageCarrier(msg)))
			assert.True(t, ok)
			mt := mocktracer.Start()
			ctx, _ := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka")
			expectedCtx, _ := tracer.SetDataStreamsCheckpoint(ctx, "group:"+testGroupID, "direction:in", "topic:"+testTopic, "type:kafka")
			expected, _ := datastreams.PathwayFromContext(expectedCtx)
			mt.Stop()
			assert.NotEqual(t, expected.GetHash(), 0)
			assert.Equal(t, expected.GetHash(), p.GetHash())
		})
	}
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
	}, WithCustomTag("foo", func(msg *kafka.Message) interface{} {
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
	assert.Equal(t, []byte("key1"), s.Tag("key"))
}

func TestNamingSchema(t *testing.T) {
	genSpans := func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []Option
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		consumerAction := consumerActionFn(func(c *Consumer) (*kafka.Message, error) {
			return c.ReadMessage(3000 * time.Millisecond)
		})
		spans, _ := produceThenConsume(t, consumerAction, opts, opts)
		return spans
	}
	namingschematest.NewKafkaTest(genSpans)(t)
}
