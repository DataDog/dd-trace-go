// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	kafkaBrokers = []string{"localhost:9092"}
)

const (
	testGroupID = "gotest_ibm_sarama"
	testTopic   = "gotest_ibm_sarama"
)

func TestNamingSchema(t *testing.T) {
	namingschematest.NewKafkaTest(genTestSpans)(t)
}

func genTestSpans(t *testing.T, serviceOverride string) []mocktracer.Span {
	cfg := newIntegrationTestConfig(t)

	var opts []Option
	if serviceOverride != "" {
		opts = append(opts, WithServiceName(serviceOverride))
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, opts...)

	c, err := sarama.NewConsumer(kafkaBrokers, cfg)
	require.NoError(t, err)
	defer func(c sarama.Consumer) {
		err := c.Close()
		require.NoError(t, err)
	}(c)
	c = WrapConsumer(c, opts...)

	msg1 := &sarama.ProducerMessage{
		Topic:    "test-topic",
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	pc, err := c.ConsumePartition("test-topic", 0, 0)
	require.NoError(t, err)
	_ = <-pc.Messages()
	err = pc.Close()
	require.NoError(t, err)
	// wait for the channel to be closed
	<-pc.Messages()
	waitForSpans(mt, 2)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	return spans
}

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
		ctx, _ = tracer.SetDataStreamsCheckpoint(context.Background(), "direction:out", "topic:"+testTopic, "type:kafka")
	}
	ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, edgeTags...)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, want.GetHash(), 0)
	assert.Equal(t, want.GetHash(), got.GetHash())
}
