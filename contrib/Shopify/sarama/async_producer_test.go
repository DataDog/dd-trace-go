// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sarama

import (
	"context"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

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

func TestWrapAsyncProducerIgnoresNilMessage(t *testing.T) {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	raw := newBlockedAsyncProducer()

	mt := mocktracer.Start()
	defer mt.Stop()

	producer := WrapAsyncProducer(cfg, raw)
	producer.Input() <- nil
	msg := &sarama.ProducerMessage{Topic: "topic"}
	producer.Input() <- msg

	select {
	case got := <-raw.input:
		require.Same(t, msg, got)
	case <-time.After(time.Second):
		t.Fatal("wrapped producer did not forward the message after the nil one")
	}
	require.Eventually(t, func() bool {
		return len(mt.FinishedSpans()) == 1
	}, time.Second, 10*time.Millisecond)

	close(raw.successes)
	assertWrappedSuccessesClosed(t, producer)
}
