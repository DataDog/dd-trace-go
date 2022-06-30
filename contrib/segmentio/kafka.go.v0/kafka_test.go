// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

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

const (
	testGroupID = "gosegtest"
	testTopic   = "gosegtest"
)

func skipIntegrationTest(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
}

/*
to setup the integration test locally run:
	docker-compose -f local_testing.yaml up
*/

func TestConsumerFunctional(t *testing.T) {
	// skipIntegrationTest(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	kw := &kafka.Writer{
		Addr:         kafka.TCP("localhost:9092"),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
	}

	w := WrapWriter(kw, WithAnalyticsRate(0.1))
	msg1 := []kafka.Message{
		{
			Key:   []byte("key1"),
			Value: []byte("value1"),
		},
	}
	err := w.WriteMessages(context.Background(), msg1...)
	assert.NoError(t, err, "Expected to write message to topic")
	err = w.Close()
	assert.NoError(t, err)

	tctx, _ := context.WithTimeout(context.Background(), 30*time.Second)
	r := NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		GroupID: testGroupID,
		Topic:   testTopic,
	})
	msg2, err := r.ReadMessage(tctx)
	assert.NoError(t, err, "Expected to consume message")
	assert.Equal(t, msg1[0].Value, msg2.Value, "Values should be equal")
	r.Close()

	t2ctx, _ := context.WithTimeout(context.Background(), 30*time.Second)
	r2 := NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092"},
		GroupID: testGroupID + "reader-2",
		Topic:   testTopic,
	})
	msg3, err := r2.FetchMessage(t2ctx)
	assert.NoError(t, err, "Expected to fetch message")
	assert.Equal(t, msg1[0].Value, msg3.Value, "Values should be equal")

	err = r2.CommitMessages(t2ctx, msg3)
	assert.NoError(t, err, "Expected to commit message")
	r2.Close()

	// now verify the spans
	spans := mt.FinishedSpans()
	assert.Len(t, spans, 4)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Trace IDs should match")

	s0 := spans[0] // produce
	assert.Equal(t, "kafka.produce", s0.OperationName())
	assert.Equal(t, "kafka", s0.Tag(ext.ServiceName))
	assert.Equal(t, "Produce Topic "+testTopic, s0.Tag(ext.ResourceName))
	assert.Equal(t, 0.1, s0.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s0.Tag(ext.SpanType))
	assert.Equal(t, 0, s0.Tag("partition"))

	s1 := spans[1] // consume
	assert.Equal(t, "kafka.consume", s1.OperationName())
	assert.Equal(t, "kafka", s1.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic, s1.Tag(ext.ResourceName))
	assert.Equal(t, nil, s1.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s1.Tag(ext.SpanType))
	assert.Equal(t, 0, s1.Tag("partition"))

	s3 := spans[2] // consume (fetch message)
	assert.Equal(t, "kafka.consume", s3.OperationName())
	assert.Equal(t, "kafka", s3.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic+" FetchMessage", s3.Tag(ext.ResourceName))
	assert.Equal(t, nil, s3.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s3.Tag(ext.SpanType))
	assert.Equal(t, 0, s3.Tag("partition"))

	s4 := spans[3] // consume (commit message)
	assert.Equal(t, "kafka.consume", s4.OperationName())
	assert.Equal(t, "kafka", s4.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+testTopic+" CommitMessages", s4.Tag(ext.ResourceName))
	assert.Equal(t, nil, s4.Tag(ext.EventSampleRate))
	assert.Equal(t, "queue", s4.Tag(ext.SpanType))
	assert.Equal(t, 0, s4.Tag("partition"))
}
