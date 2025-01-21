// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	kafkaBrokers = []string{"localhost:9092"}
)

func newIntegrationTestConfig(t *testing.T) *sarama.Config {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0 // first version that supports headers
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 1
	return cfg
}

// waitForSpans polls the mock tracer until the expected number of spans
// appear
func waitForSpans(mt mocktracer.Tracer, sz int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	for len(mt.FinishedSpans()) < sz {
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(time.Millisecond * 100)
	}
}

func assertDSMProducerPathway(t *testing.T, topic string, msg *sarama.ProducerMessage) {
	t.Helper()

	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(),
		NewProducerMessageCarrier(msg),
	))
	require.True(t, ok, "pathway not found in kafka message")

	ctx, _ := tracer.SetDataStreamsCheckpoint(
		context.Background(),
		"direction:out", "topic:"+topic, "type:kafka",
	)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, want.GetHash(), 0)
	assert.Equal(t, want.GetHash(), got.GetHash())
}

func assertDSMConsumerPathway(t *testing.T, topic, groupID string, msg *sarama.ConsumerMessage, withProducer bool) {
	t.Helper()

	carrier := NewConsumerMessageCarrier(msg)
	got, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(),
		carrier,
	))
	require.True(t, ok, "pathway not found in kafka message")

	edgeTags := []string{"direction:in", "topic:" + topic, "type:kafka"}
	if groupID != "" {
		edgeTags = append(edgeTags, "group:"+groupID)
	}

	ctx := context.Background()
	if withProducer {
		ctx, _ = tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+topicName(t), "type:kafka")
	}
	ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, edgeTags...)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, want.GetHash(), 0)
	assert.Equal(t, want.GetHash(), got.GetHash())
}

// topicName returns a unique topic name for the current test, which ensures results are not affected by each other.
func topicName(t *testing.T) string {
	return strings.ReplaceAll("IBM/sarama/"+t.Name(), "/", "_")
}
