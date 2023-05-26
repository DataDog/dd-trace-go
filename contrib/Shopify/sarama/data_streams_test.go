// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"testing"

	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
)

func TestTraceKafkaProduce(t *testing.T) {
	t.Run("Checkpoint should be created and pathway should be propagated to kafka headers", func(t *testing.T) {
		ctx := context.Background()
		initialPathway := datastreams.NewPathway()
		ctx = datastreams.ContextWithPathway(ctx, initialPathway)

		msg := sarama.ProducerMessage{
			Topic: "test",
		}

		ctx = TraceKafkaProduce(ctx, &msg)

		// The old pathway shouldn't be equal to the new pathway found in the ctx because we created a new checkpoint.
		ctxPathway, _ := datastreams.PathwayFromContext(ctx)
		assertPathwayNotEqual(t, initialPathway, ctxPathway)

		// The decoded pathway found in the kafka headers should be the same as the pathway found in the ctx.
		var encodedPathway []byte
		for _, header := range msg.Headers {
			if string(header.Key) == datastreams.PropagationKey {
				encodedPathway = header.Value
			}
		}
		headersPathway, _ := datastreams.Decode(encodedPathway)
		assertPathwayEqual(t, ctxPathway, headersPathway)
	})
}

func TestTraceKafkaConsume(t *testing.T) {
	t.Run("Checkpoint should be created and pathway should be extracted from kafka headers into context", func(t *testing.T) {
		// First, set up pathway and context as it would have been from the producer view.
		producerCtx := context.Background()
		initialPathway := datastreams.NewPathway()
		producerCtx = datastreams.ContextWithPathway(producerCtx, initialPathway)

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
		consumerCtxPathway, _ := datastreams.PathwayFromContext(consumerCtx)
		_, expectedCtx := datastreams.SetCheckpoint(producerCtx, "direction:in", "group:my-consumer-group", "topic:my-topic", "type:kafka")
		expectedCtxPathway, _ := datastreams.PathwayFromContext(expectedCtx)
		assertPathwayEqual(t, expectedCtxPathway, consumerCtxPathway)
	})
}

func assertPathwayNotEqual(t *testing.T, p1 datastreams.Pathway, p2 datastreams.Pathway) {
	decodedP1, err1 := datastreams.Decode(p1.Encode())
	decodedP2, err2 := datastreams.Decode(p2.Encode())

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	assert.NotEqual(t, decodedP1, decodedP2)
}

func assertPathwayEqual(t *testing.T, p1 datastreams.Pathway, p2 datastreams.Pathway) {
	decodedP1, err1 := datastreams.Decode(p1.Encode())
	decodedP2, err2 := datastreams.Decode(p2.Encode())

	assert.Nil(t, err1)
	assert.Nil(t, err2)
	assert.Equal(t, decodedP1, decodedP2)
}
