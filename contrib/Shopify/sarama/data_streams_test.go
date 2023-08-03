// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/dsminterface"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/assert"
)

func TestTraceKafkaProduce(t *testing.T) {
	t.Run("Checkpoint should be created and pathway should be propagated to kafka headers", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		initialPathway, ctx := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:in", "type:rabbitmq")

		msg := sarama.ProducerMessage{
			Topic: "test",
		}

		ctx = TraceKafkaProduce(ctx, &msg)

		// The old pathway shouldn't be equal to the new pathway found in the ctx because we created a new checkpoint.
		ctxPathway := datastreams.PathwayFromContext(ctx)
		assertPathwayNotEqual(t, initialPathway, ctxPathway)

		// The decoded pathway found in the kafka headers should be the same as the pathway found in the ctx.
		var encodedPathway []byte
		for _, header := range msg.Headers {
			if string(header.Key) == datastreams.PropagationKey {
				encodedPathway = header.Value
			}
		}
		headersPathway, _, _ := datastreams.Decode(context.Background(), encodedPathway)
		assertPathwayEqual(t, ctxPathway, headersPathway)
	})
}

func TestTraceKafkaConsume(t *testing.T) {
	t.Run("Checkpoint should be created and pathway should be extracted from kafka headers into context", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		_, producerCtx := tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:topic1")

		topic := "my-topic"
		produceMessage := sarama.ProducerMessage{
			Topic: topic,
		}
		producerCtx = TraceKafkaProduce(producerCtx, &produceMessage)
		consumeMessage := sarama.ConsumerMessage{
			Topic: topic,
		}
		for _, header := range produceMessage.Headers {
			consumeMessage.Headers = append(consumeMessage.Headers, &sarama.RecordHeader{Key: header.Key, Value: header.Value})
		}
		// Calls TraceKafkaConsume
		group := "my-consumer-group"
		consumerCtx := context.Background()
		consumerCtx = TraceKafkaConsume(consumerCtx, &consumeMessage, group)

		// Check that the resulting consumerCtx contains an expected pathway.
		consumerCtxPathway := datastreams.PathwayFromContext(consumerCtx)
		_, expectedCtx := tracer.SetDataStreamsCheckpoint(producerCtx, "direction:in", "group:my-consumer-group", "topic:my-topic", "type:kafka")
		expectedCtxPathway := datastreams.PathwayFromContext(expectedCtx)
		assertPathwayEqual(t, expectedCtxPathway, consumerCtxPathway)
	})
}

func assertPathwayNotEqual(t *testing.T, p1 dsminterface.Pathway, p2 dsminterface.Pathway) {
	decodedP1, _, err1 := datastreams.Decode(context.Background(), p1.Encode())
	decodedP2, _, err2 := datastreams.Decode(context.Background(), p2.Encode())

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	assert.NotEqual(t, decodedP1, decodedP2)
}

func assertPathwayEqual(t *testing.T, p1 dsminterface.Pathway, p2 dsminterface.Pathway) {
	decodedP1, _, err1 := datastreams.Decode(context.Background(), p1.Encode())
	decodedP2, _, err2 := datastreams.Decode(context.Background(), p2.Encode())

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	assert.Equal(t, decodedP1, decodedP2)
}
