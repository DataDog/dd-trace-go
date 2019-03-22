package kafka

import (
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/stretchr/testify/assert"
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
	}, WithAnalyticsRate(0.3))
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
		assert.Equal(t, int32(1), s.Tag("partition"))
		assert.Equal(t, 0.3, s.Tag(ext.EventSampleRate))
		assert.Equal(t, kafka.Offset(i+1), s.Tag("offset"))
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

func TestConsumerPoll(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}

	mt := mocktracer.Start()
	defer mt.Stop()

	// first write a message to the topic

	p, err := NewProducer(&kafka.ConfigMap{
		"group.id":            testGroupID,
		"bootstrap.servers":   "127.0.0.1:9092",
		"go.delivery.reports": true,
	}, WithAnalyticsRate(0.1))
	assert.NoError(t, err)
	delivery := make(chan kafka.Event, 1)
	err = p.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &testTopic,
			Partition: 0,
		},
		Key:   []byte("key2"),
		Value: []byte("value2"),
	}, delivery)
	assert.NoError(t, err)
	msg1, _ := (<-delivery).(*kafka.Message)
	p.Close()

	// next attempt to consume the message

	c, err := NewConsumer(&kafka.ConfigMap{
		"group.id":                 testGroupID,
		"bootstrap.servers":        "127.0.0.1:9092",
		"socket.timeout.ms":        1000,
		"session.timeout.ms":       1000,
		"enable.auto.offset.store": false,
	})
	assert.NoError(t, err)

	err = c.Assign([]kafka.TopicPartition{
		{Topic: &testTopic, Partition: 0, Offset: msg1.TopicPartition.Offset},
	})
	assert.NoError(t, err)

	msg2, _ := c.Poll(3000).(*kafka.Message)
	assert.Equal(t, msg1.String(), msg2.String())

	c.Close()

	// now verify the spans
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	s0 := spans[0] // produce
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic gotest", s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, int32(0), s0.Tag("partition"))

	s1 := spans[1] // consume
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic gotest", s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, int32(0), s1.Tag("partition"))
}
