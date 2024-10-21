// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kafkatrace "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var confluentKafkaV2 = harness.KafkaTestCase(
	instrumentation.PackageConfluentKafkaGoV2,
	func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		mt := mocktracer.Start()
		defer mt.Stop()

		var (
			testGroupID = "gotest"
			testTopic   = "gotest"
		)

		var opts []kafkatrace.OptionFn
		if serviceOverride != "" {
			opts = append(opts, kafkatrace.WithService.apply(serviceOverride))
		}
		p, err := kafkatrace.NewProducer(&kafka.ConfigMap{
			"bootstrap.servers":   "127.0.0.1:9092",
			"go.delivery.reports": true,
		}, opts...)
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
		c, err := kafkatrace.NewConsumer(&kafka.ConfigMap{
			"group.id":                 testGroupID,
			"bootstrap.servers":        "127.0.0.1:9092",
			"fetch.wait.max.ms":        500,
			"socket.timeout.ms":        1500,
			"session.timeout.ms":       1500,
			"enable.auto.offset.store": false,
		}, opts...)
		require.NoError(t, err)

		err = c.Assign([]kafka.TopicPartition{
			{Topic: &testTopic, Partition: 0, Offset: msg1.TopicPartition.Offset},
		})
		require.NoError(t, err)

		msg2, err := c.ReadMessage(3000 * time.Millisecond)
		require.NoError(t, err)
		_, err = c.CommitMessage(msg2)
		require.NoError(t, err)
		assert.Equal(t, msg1.String(), msg2.String())
		err = c.Close()
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
)
