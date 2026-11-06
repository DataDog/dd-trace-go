// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestSyncProducer(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))

		assertDSMProducerPathway(t, topic, msg1)
	}
}

func TestSyncProducerSendMessages(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(cfg, producer, WithDataStreams())
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	msg2 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 2"),
		Metadata: "test",
	}
	err = producer.SendMessages([]*sarama.ProducerMessage{msg1, msg2})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	for _, s := range spans {
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))
	}

	for _, msg := range []*sarama.ProducerMessage{msg1, msg2} {
		assertDSMProducerPathway(t, topic, msg)
	}
}

func TestSyncProducerWithCustomSpanOptions(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)
	producer = WrapSyncProducer(
		cfg,
		producer,
		WithDataStreams(),
		WithProducerCustomTag(
			"messaging.kafka.key",
			func(msg *sarama.ProducerMessage) any {
				key, err := msg.Key.Encode()
				assert.NoError(t, err)

				return string(key)
			},
		),
	)
	defer func() {
		assert.NoError(t, producer.Close())
	}()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Key:      sarama.StringEncoder("test key"),
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = producer.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	{
		s := spans[0]
		assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
		assert.Equal(t, "queue", s.Tag(ext.SpanType))
		assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
		assert.Equal(t, "kafka.produce", s.OperationName())
		assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
		assert.NotNil(t, s.Tag("offset"))
		assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
		assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
		assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
		assert.Equal(t, topic, s.Tag("messaging.destination.name"))
		assert.Equal(t, "test key", s.Tag("messaging.kafka.key"))

		assertDSMProducerPathway(t, topic, msg1)
	}
}

func TestWrapAsyncProducer(t *testing.T) {
	// the default for producers is a fire-and-forget model that doesn't return
	// successes
	t.Run("Without Successes", func(t *testing.T) {
		cfg := newIntegrationTestConfig(t)
		cfg.Producer.Return.Successes = false
		topic := topicName(t)

		mt := mocktracer.Start()
		defer mt.Stop()

		producer, err := sarama.NewAsyncProducer(kafkaBrokers, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(cfg, producer, WithDataStreams())
		defer func() {
			assert.NoError(t, producer.Close())
		}()

		msg1 := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1

		waitForSpans(t, mt, 1, 5*time.Second)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())

			// these tags are set in the finishProducerSpan function, but in this case it's never used, and instead we
			// automatically finish spans after being started because we don't have a way to know when they are finished.
			assert.Nil(t, s.Tag(ext.MessagingKafkaPartition))
			assert.Nil(t, s.Tag("offset"))

			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
			assert.Equal(t, topic, s.Tag("messaging.destination.name"))

			assertDSMProducerPathway(t, topic, msg1)
		}
	})

	t.Run("With Successes", func(t *testing.T) {
		cfg := newIntegrationTestConfig(t)
		cfg.Producer.Return.Successes = true
		topic := topicName(t)

		mt := mocktracer.Start()
		defer mt.Stop()

		producer, err := sarama.NewAsyncProducer(kafkaBrokers, cfg)
		require.NoError(t, err)
		producer = WrapAsyncProducer(cfg, producer, WithDataStreams())
		defer func() {
			assert.NoError(t, producer.Close())
		}()

		msg1 := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.StringEncoder("test 1"),
		}
		producer.Input() <- msg1
		<-producer.Successes()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		{
			s := spans[0]
			assert.Equal(t, "kafka", s.Tag(ext.ServiceName))
			assert.Equal(t, "queue", s.Tag(ext.SpanType))
			assert.Equal(t, "Produce Topic "+topic, s.Tag(ext.ResourceName))
			assert.Equal(t, "kafka.produce", s.OperationName())
			assert.Equal(t, float64(0), s.Tag(ext.MessagingKafkaPartition))
			assert.NotNil(t, s.Tag("offset"))
			assert.Equal(t, "IBM/sarama", s.Tag(ext.Component))
			assert.Equal(t, ext.SpanKindProducer, s.Tag(ext.SpanKind))
			assert.Equal(t, "kafka", s.Tag(ext.MessagingSystem))
			assert.Equal(t, topic, s.Tag("messaging.destination.name"))

			assertDSMProducerPathway(t, topic, msg1)
		}
	})
}

func TestWrapAsyncProducerDrainsSuccessesWhileInputIsBlocked(t *testing.T) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	cfg.Producer.Return.Successes = true
	raw := &blockedAsyncProducer{
		input:     make(chan *sarama.ProducerMessage),
		successes: make(chan *sarama.ProducerMessage),
		errors:    make(chan *sarama.ProducerError),
	}
	producer := WrapAsyncProducer(cfg, raw)

	blocked := &sarama.ProducerMessage{Topic: "blocked"}
	producer.Input() <- blocked

	completed := &sarama.ProducerMessage{Topic: "completed"}
	go func() {
		raw.successes <- completed
	}()

	select {
	case msg := <-producer.Successes():
		require.Same(t, completed, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not drain the underlying success")
	}

	select {
	case msg := <-raw.input:
		require.Same(t, blocked, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not forward the pending input")
	}

	go func() {
		raw.successes <- blocked
	}()

	select {
	case msg := <-producer.Successes():
		require.Same(t, blocked, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not return the pending input success")
	}

	close(raw.successes)
	select {
	case _, ok := <-producer.Successes():
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not shut down after the underlying producer closed")
	}
}

func TestWrapAsyncProducerFinishesSpanOnDeliveryNotIntake(t *testing.T) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	raw := newBlockedAsyncProducer()

	mt := mocktracer.Start()
	defer mt.Stop()

	producer := WrapAsyncProducer(cfg, raw)
	blocked := &sarama.ProducerMessage{Topic: "blocked"}
	producer.Input() <- blocked

	require.Never(t, func() bool {
		return len(mt.FinishedSpans()) > 0
	}, 250*time.Millisecond, 25*time.Millisecond)

	select {
	case msg := <-raw.input:
		require.Same(t, blocked, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not forward the pending input")
	}

	require.Eventually(t, func() bool {
		return len(mt.FinishedSpans()) == 1
	}, time.Second, 10*time.Millisecond)
	span := mt.FinishedSpans()[0]
	assert.Equal(t, "kafka.produce", span.OperationName())
	assert.Nil(t, span.Tag(ext.MessagingKafkaPartition))
	assert.Nil(t, span.Tag("offset"))
	assert.Nil(t, span.Tag(ext.ErrorMsg))

	close(raw.successes)
	assertWrappedSuccessesClosed(t, producer)
}

func TestWrapAsyncProducerDrainsErrorsWhileInputIsBlocked(t *testing.T) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	cfg.Producer.Return.Successes = true
	raw := newBlockedAsyncProducer()

	mt := mocktracer.Start()
	defer mt.Stop()

	producer := WrapAsyncProducer(cfg, raw)
	first := &sarama.ProducerMessage{Topic: "first"}
	producer.Input() <- first
	select {
	case msg := <-raw.input:
		require.Same(t, first, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not forward the first message")
	}

	blocked := &sarama.ProducerMessage{Topic: "blocked"}
	producer.Input() <- blocked
	producerError := &sarama.ProducerError{Msg: first, Err: context.Canceled}
	go func() {
		raw.errors <- producerError
	}()

	select {
	case err := <-producer.Errors():
		require.Same(t, producerError, err)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not drain the underlying error")
	}

	require.Eventually(t, func() bool {
		return len(mt.FinishedSpans()) == 1
	}, time.Second, 10*time.Millisecond)
	span := mt.FinishedSpans()[0]
	assert.Equal(t, "kafka.produce", span.OperationName())
	assert.Equal(t, producerError.Error(), span.Tag(ext.ErrorMsg))

	select {
	case msg := <-raw.input:
		require.Same(t, blocked, msg)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not forward the pending input after the error")
	}

	close(raw.errors)
	close(raw.successes)
	assertWrappedSuccessesClosed(t, producer)
}

func TestWrapAsyncProducerFinishesPendingSpanOnClose(t *testing.T) {
	for _, exit := range []struct {
		name  string
		close func(*blockedAsyncProducer)
	}{
		{"successes closed", func(raw *blockedAsyncProducer) { close(raw.successes) }},
		{"errors closed", func(raw *blockedAsyncProducer) { close(raw.errors) }},
	} {
		t.Run(exit.name, func(t *testing.T) {
			cfg := sarama.NewConfig()
			cfg.Version = sarama.V0_11_0_0
			cfg.Producer.Return.Successes = true
			raw := newBlockedAsyncProducer()

			mt := mocktracer.Start()
			defer mt.Stop()

			producer := WrapAsyncProducer(cfg, raw)
			producer.Input() <- &sarama.ProducerMessage{Topic: "blocked"}
			exit.close(raw)
			assertWrappedSuccessesClosed(t, producer)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			assert.Equal(t, sarama.ErrShuttingDown.Error(), spans[0].Tag(ext.ErrorMsg))
		})
	}
}

var errProduceFailed = errors.New("failed to produce message")

// errorCheckTestCases are shared by the WithErrorCheck tests below.
var errorCheckTestCases = []struct {
	name     string
	errCheck func(error) bool
	// wantErr is whether the produced spans should be marked as errored.
	wantErr bool
}{
	{name: "errCheck true", errCheck: func(error) bool { return true }, wantErr: true},
	{name: "errCheck false", errCheck: func(error) bool { return false }, wantErr: false},
}

func TestSyncProducerWithErrorCheck(t *testing.T) {
	for _, tc := range errorCheckTestCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cfg := sarama.NewConfig()
			cfg.Version = sarama.V0_11_0_0
			raw := &failingSyncProducer{err: errProduceFailed}
			producer := WrapSyncProducer(cfg, raw, WithErrorCheck(tc.errCheck))

			_, _, err := producer.SendMessage(&sarama.ProducerMessage{Topic: "test"})
			// the option only affects the span; the caller still sees the error.
			require.ErrorIs(t, err, errProduceFailed)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			if tc.wantErr {
				assert.Equal(t, errProduceFailed.Error(), spans[0].Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
			}
		})
	}
}

func TestSyncProducerSendMessagesWithErrorCheck(t *testing.T) {
	for _, tc := range errorCheckTestCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cfg := sarama.NewConfig()
			cfg.Version = sarama.V0_11_0_0
			msgs := []*sarama.ProducerMessage{{Topic: "test"}, {Topic: "test"}}
			producerErrors := sarama.ProducerErrors{
				{Msg: msgs[0], Err: errProduceFailed},
				{Msg: msgs[1], Err: errProduceFailed},
			}
			raw := &failingSyncProducer{err: producerErrors}
			producer := WrapSyncProducer(cfg, raw, WithErrorCheck(tc.errCheck))

			err := producer.SendMessages(msgs)
			require.ErrorAs(t, err, new(sarama.ProducerErrors))

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)
			for _, s := range spans {
				if tc.wantErr {
					assert.Equal(t, producerErrors[0].Error(), s.Tag(ext.ErrorMsg))
				} else {
					assert.Nil(t, s.Tag(ext.ErrorMsg))
				}
			}
		})
	}
}

func TestWrapAsyncProducerWithErrorCheck(t *testing.T) {
	for _, tc := range errorCheckTestCases {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			cfg := sarama.NewConfig()
			cfg.Version = sarama.V0_11_0_0
			cfg.Producer.Return.Successes = true
			raw := newBlockedAsyncProducer()
			producer := WrapAsyncProducer(cfg, raw, WithErrorCheck(tc.errCheck))

			msg := &sarama.ProducerMessage{Topic: "test"}
			producer.Input() <- msg
			select {
			case got := <-raw.input:
				require.Same(t, msg, got)
			case <-time.After(time.Second):
				t.Fatal("wrapped producer did not forward the message")
			}
			raw.errors <- &sarama.ProducerError{Msg: msg, Err: errProduceFailed}

			// the span is finished before the error is forwarded, so once
			// this returns the span is guaranteed to be available.
			var err *sarama.ProducerError
			select {
			case err = <-producer.Errors():
				require.ErrorIs(t, err, errProduceFailed)
			case <-time.After(time.Second):
				t.Fatal("wrapped producer did not forward the error")
			}

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			if tc.wantErr {
				assert.Equal(t, err.Error(), spans[0].Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, spans[0].Tag(ext.ErrorMsg))
			}

			close(raw.errors)
			close(raw.successes)
			assertWrappedSuccessesClosed(t, producer)
		})
	}
}

func TestSyncProducerSendMessagesWithErrorCheckPerMessage(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	errIgnored := errors.New("ignored failure")
	ignored := &sarama.ProducerMessage{Topic: "ignored"}
	failed := &sarama.ProducerMessage{Topic: "failed"}
	succeeded := &sarama.ProducerMessage{Topic: "succeeded"}
	// the real SyncProducer reports batch failures as sarama.ProducerErrors.
	raw := &failingSyncProducer{err: sarama.ProducerErrors{
		{Msg: ignored, Err: errIgnored},
		{Msg: failed, Err: errProduceFailed},
	}}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0

	producer := WrapSyncProducer(cfg, raw, WithErrorCheck(func(err error) bool {
		return !errors.Is(err, errIgnored)
	}))
	err := producer.SendMessages([]*sarama.ProducerMessage{ignored, failed, succeeded})
	require.ErrorAs(t, err, new(sarama.ProducerErrors))

	spans := mt.FinishedSpans()
	require.Len(t, spans, 3)
	errByTopic := make(map[any]any, len(spans))
	for _, s := range spans {
		errByTopic[s.Tag(ext.MessagingDestinationName)] = s.Tag(ext.ErrorMsg)
	}
	assert.Nil(t, errByTopic["ignored"])
	assert.Nil(t, errByTopic["succeeded"])
	require.NotNil(t, errByTopic["failed"])
	assert.Contains(t, errByTopic["failed"], errProduceFailed.Error())
}

type failingSyncProducer struct {
	sarama.SyncProducer
	err error
}

func (p *failingSyncProducer) SendMessage(*sarama.ProducerMessage) (int32, int64, error) {
	return 0, 0, p.err
}

func (p *failingSyncProducer) SendMessages([]*sarama.ProducerMessage) error {
	return p.err
}

type blockedAsyncProducer struct {
	sarama.AsyncProducer
	input     chan *sarama.ProducerMessage
	successes chan *sarama.ProducerMessage
	errors    chan *sarama.ProducerError
}

func newBlockedAsyncProducer() *blockedAsyncProducer {
	return &blockedAsyncProducer{
		input:     make(chan *sarama.ProducerMessage),
		successes: make(chan *sarama.ProducerMessage),
		errors:    make(chan *sarama.ProducerError),
	}
}

func assertWrappedSuccessesClosed(t *testing.T, producer sarama.AsyncProducer) {
	t.Helper()
	select {
	case _, ok := <-producer.Successes():
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not shut down after the underlying producer closed")
	}
}

func (p *blockedAsyncProducer) Input() chan<- *sarama.ProducerMessage {
	return p.input
}

func (p *blockedAsyncProducer) Successes() <-chan *sarama.ProducerMessage {
	return p.successes
}

func (p *blockedAsyncProducer) Errors() <-chan *sarama.ProducerError {
	return p.errors
}

func TestSyncProducerWithClusterID(t *testing.T) {
	cfg := newIntegrationTestConfig(t)
	topic := topicName(t)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := sarama.NewSyncProducer(kafkaBrokers, cfg)
	require.NoError(t, err)

	wrapped := WrapSyncProducer(cfg, producer, WithDataStreams(), WithBrokers(kafkaBrokers))

	// Wait for the async cluster ID fetch to complete
	require.Eventually(t, func() bool {
		return wrapped.(*syncProducer).cfg.ClusterID() != ""
	}, 5*time.Second, 10*time.Millisecond)

	clusterID := wrapped.(*syncProducer).cfg.ClusterID()

	msg1 := &sarama.ProducerMessage{
		Topic:    topic,
		Value:    sarama.StringEncoder("test 1"),
		Metadata: "test",
	}
	_, _, err = wrapped.SendMessage(msg1)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	s := spans[0]

	// Verify cluster ID is set as a span tag
	assert.Equal(t, clusterID, s.Tag(ext.MessagingKafkaClusterID))
	assert.NotEmpty(t, clusterID)

	// Verify DSM pathway hash includes kafka_cluster_id in edge tags
	p, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(
		context.Background(), NewProducerMessageCarrier(msg1),
	))
	require.True(t, ok, "pathway not found in kafka message")

	expectedCtx, _ := tracer.SetDataStreamsCheckpoint(
		context.Background(),
		"direction:out", "topic:"+topic, "type:kafka", "kafka_cluster_id:"+clusterID,
	)
	expected, _ := datastreams.PathwayFromContext(expectedCtx)
	assert.NotEqual(t, expected.GetHash(), 0)
	assert.Equal(t, expected.GetHash(), p.GetHash())

	assert.NoError(t, wrapped.Close())
}

// TestStartProducerSpanCustomTagPrecedence is a regression test for a Codex
// review finding on PR #5007: WithProducerCustomTag lets a caller override a
// tag also set by the cached spanCfg base (e.g. component, span.kind).
// Custom tags must win on key collision, matching pre-migration behavior
// where custom-tag options were appended last in the option list. Uses
// startProducerSpan directly, no live broker needed.
func TestStartProducerSpanCustomTagPrecedence(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	cfg := new(config)
	defaults(cfg)
	cfg.producerCustomTags[ext.Component] = func(*sarama.ProducerMessage) any {
		return "custom-component"
	}
	spanCfg := newProducerSpanConfig(cfg)
	msg := &sarama.ProducerMessage{Topic: "test-topic"}

	span := startProducerSpan(cfg, spanCfg, sarama.MinVersion, msg)
	span.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "custom-component", spans[0].Tag(ext.Component))
}
