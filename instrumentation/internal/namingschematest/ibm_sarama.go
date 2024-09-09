// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	saramatrace "github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ibmSaramaGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []saramatrace.Option
		if serviceOverride != "" {
			opts = append(opts, saramatrace.WithService(serviceOverride))
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
		producer = saramatrace.WrapSyncProducer(cfg, producer, opts...)

		c, err := sarama.NewConsumer([]string{broker.Addr()}, cfg)
		require.NoError(t, err)
		defer func(c sarama.Consumer) {
			err := c.Close()
			require.NoError(t, err)
		}(c)
		c = saramatrace.WrapConsumer(c, opts...)

		msg1 := &sarama.ProducerMessage{
			Topic:    "test-topic",
			Value:    sarama.StringEncoder("test 1"),
			Metadata: "test",
		}
		_, _, err = producer.SendMessage(msg1)
		require.NoError(t, err)

		pc, err := c.ConsumePartition("test-topic", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = <-pc.Messages()
		err = pc.Close()
		require.NoError(t, err)
		// wait for the channel to be closed
		<-pc.Messages()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)
		return spans
	}
}

var ibmSarama = harness.TestCase{
	Name:     instrumentation.PackageIBMSarama,
	GenSpans: ibmSaramaGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("kafka", 2),
		DDService:       []string{"kafka", harness.TestDDService},
		ServiceOverride: harness.RepeatString(harness.TestServiceOverride, 2),
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "kafka.produce", spans[0].OperationName())
		assert.Equal(t, "kafka.consume", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "kafka.send", spans[0].OperationName())
		assert.Equal(t, "kafka.process", spans[1].OperationName())
	},
}
