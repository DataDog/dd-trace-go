package kafka

import (
	"context"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	kafka "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
)

var (
	testGroupID = "kafkagotest"
	testTopic   = "gotest"
)

func skipIntegrationTest(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
}

/*
to run the integration test locally:

    docker network create segementio

    docker run --rm \
        --name zookeeper \
        --network segementio \
        -p 2181:2181 \
        wurstmeister/zookeeper:3.4.6

    docker run --rm \
        --name kafka \
        --network segementio \
        -p 9092:9092 \
        -e KAFKA_ZOOKEEPER_CONNECT=zookeeper:2181 \
        -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:9092 \
        -e KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:9092 \
        -e KAFKA_CREATE_TOPICS=gotest:1:1 \
        -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
        wurstmeister/kafka:2.13-2.7.0
*/

func TestConsumerFunctional(t *testing.T) {
	skipIntegrationTest(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	w := NewWriter(kafka.WriterConfig{
		Brokers: []string{"127.0.0.1:9092"},
		Topic:   testTopic,
	}, WithAnalyticsRate(0.1))
	msg1 := []kafka.Message{
		{
			Key:   []byte("key1"),
			Value: []byte("value1"),
		},
	}
	err := w.WriteMessages(context.Background(), msg1...)
	assert.NoError(t, err, "Expected to write message to topic")
	w.Close()

	r := NewReader(kafka.ReaderConfig{
		Brokers:        []string{"127.0.0.1:9092"},
		GroupID:        testGroupID,
		Topic:          testTopic,
		SessionTimeout: 30 * time.Second,
		StartOffset:    kafka.LastOffset,
	})
	msg2, err := r.ReadMessage(context.Background())
	assert.NoError(t, err, "Expected to consume message")
	assert.Equal(t, msg1[0].Value, msg2.Value, "Values should be equal")
	r.Close()

	// now verify the spans
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Trace IDs should match")

	s0 := spans[0] // produce
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic gotest", s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, 0, s0.Tag("partition"))
}
