// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"
	"time"

	segmentiotracer "github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testGroupID       = "gosegtest"
	testTopic         = "gosegtest"
	testReaderMaxWait = 10 * time.Millisecond
)

type readerOpFn func(t *testing.T, r *segmentiotracer.Reader)

func genIntegrationTestSpans(t *testing.T, mt mocktracer.Tracer, writerOp func(t *testing.T, w *segmentiotracer.KafkaWriter), readerOp readerOpFn, writerOpts []segmentiotracer.Option, readerOpts []segmentiotracer.Option) ([]*mocktracer.Span, []kafka.Message) {
	writtenMessages := []kafka.Message{}

	// add some dummy values to broker/addr to test bootstrap servers.
	kw := &kafka.Writer{
		Addr:         kafka.TCP("localhost:9092", "localhost:9093", "localhost:9094"),
		Topic:        testTopic,
		RequiredAcks: kafka.RequireOne,
		Completion: func(messages []kafka.Message, err error) {
			writtenMessages = append(writtenMessages, messages...)
		},
	}
	w := segmentiotracer.WrapWriter(kw, writerOpts...)
	writerOp(t, w)
	err := w.Close()
	require.NoError(t, err)

	r := segmentiotracer.NewReader(kafka.ReaderConfig{
		Brokers: []string{"localhost:9092", "localhost:9093", "localhost:9094"},
		GroupID: testGroupID,
		Topic:   testTopic,
		MaxWait: testReaderMaxWait,
	}, readerOpts...)
	readerOp(t, r)
	err = r.Close()
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	// they should be linked via headers
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID(), "Trace IDs should match")
	return spans, writtenMessages
}

func segmentioKafkaGoGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []segmentiotracer.Option
		if serviceOverride != "" {
			opts = append(opts, segmentiotracer.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()

		messagesToWrite := []kafka.Message{
			{
				Key:   []byte("key1"),
				Value: []byte("value1"),
			},
		}

		spans, _ := genIntegrationTestSpans(
			t,
			mt,
			func(t *testing.T, w *segmentiotracer.KafkaWriter) {
				err := w.WriteMessages(context.Background(), messagesToWrite...)
				require.NoError(t, err, "Expected to write message to topic")
			},
			func(t *testing.T, r *segmentiotracer.Reader) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				readMsg, err := r.FetchMessage(ctx)
				require.NoError(t, err, "Expected to consume message")
				assert.Equal(t, messagesToWrite[0].Value, readMsg.Value, "Values should be equal")

				err = r.CommitMessages(context.Background(), readMsg)
				assert.NoError(t, err, "Expected CommitMessages to not return an error")
			},
			opts,
			opts,
		)
		return spans
	}
}

var segmentioKafkaGo = harness.TestCase{
	Name:     instrumentation.PackageSegmentioKafkaGo,
	GenSpans: segmentioKafkaGoGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        harness.RepeatString("kafka", 2),
		DDService:       harness.RepeatString(harness.TestDDService, 2),
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
