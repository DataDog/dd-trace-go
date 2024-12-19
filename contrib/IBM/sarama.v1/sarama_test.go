// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var kafkaBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}

const (
	testGroupID = "gotest"
	testTopic   = "gotest"
)

func TestNamingSchema(t *testing.T) {
	namingschematest.NewKafkaTest(genTestSpans)(t)
}

func genTestSpans(t *testing.T, serviceOverride string) []mocktracer.Span {
	var opts []Option
	if serviceOverride != "" {
		opts = append(opts, WithServiceName(serviceOverride))
	}
	mt := mocktracer.Start()
	defer mt.Stop()

	broker := sarama.NewMockBroker(t, 1)
	defer broker.Close()

	broker.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(broker.Addr(), broker.BrokerID()).
			SetLeader("test-topic", 0, broker.BrokerID()),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset("test-topic", 0, sarama.OffsetOldest, 0).
			SetOffset("test-topic", 0, sarama.OffsetNewest, 1),
		"FetchRequest": sarama.NewMockFetchResponse(t, 1).
			SetMessage("test-topic", 0, 0, sarama.StringEncoder("hello")),
		"ProduceRequest": sarama.NewMockProduceResponse(t).
			SetError("test-topic", 0, sarama.ErrNoError),
	})
	cfg := sarama.NewConfig()
	cfg.Version = sarama.MinVersion
	cfg.Producer.Return.Successes = true
	cfg.Producer.Flush.Messages = 1

	producer, err := sarama.NewSyncProducer([]string{broker.Addr()}, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, opts...)

	c, err := sarama.NewConsumer([]string{broker.Addr()}, cfg)
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

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	return spans
}

func newMockBroker(t *testing.T) *sarama.MockBroker {
	broker := sarama.NewMockBroker(t, 1)

	metadataResponse := new(sarama.MetadataResponse)
	metadataResponse.Version = 1
	metadataResponse.AddBroker(broker.Addr(), broker.BrokerID())
	metadataResponse.AddTopicPartition("my_topic", 0, broker.BrokerID(), nil, nil, nil, sarama.ErrNoError)
	broker.Returns(metadataResponse)

	prodSuccess := new(sarama.ProduceResponse)
	prodSuccess.Version = 2
	prodSuccess.AddTopicPartition("my_topic", 0, sarama.ErrNoError)
	for i := 0; i < 10; i++ {
		broker.Returns(prodSuccess)
	}
	return broker
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

	//ctx, _ := tracer.SetDataStreamsCheckpointWithParams(
	//	datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
	//	options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)},
	//	edgeTags...,
	//)

	ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, edgeTags...)
	want, _ := datastreams.PathwayFromContext(ctx)

	assert.NotEqual(t, want.GetHash(), 0)
	assert.Equal(t, want.GetHash(), got.GetHash())
}
