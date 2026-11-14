// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kafkamock starts an in-process Kafka broker for the sarama tests
// that count spans, so they need no container.
package kafkamock

import (
	"errors"
	"fmt"
	"time"

	"github.com/IBM/sarama"
)

const (
	Topic     = "ibm_sarama_mock_test"
	Partition = int32(0)
	Message   = "Hello, World!"

	timeout = 15 * time.Second
)

// Start starts a broker that leads Topic, accepts every produced message, and
// serves Message at offset 0 of Partition. The caller closes it.
func Start(t sarama.TestReporter) *sarama.MockBroker {
	b := sarama.NewMockBroker(t, 1)
	b.SetHandlerByMap(map[string]sarama.MockResponse{
		"MetadataRequest": sarama.NewMockMetadataResponse(t).
			SetBroker(b.Addr(), b.BrokerID()).
			SetLeader(Topic, Partition, b.BrokerID()),
		"ProduceRequest": sarama.NewMockProduceResponse(t),
		"OffsetRequest": sarama.NewMockOffsetResponse(t).
			SetOffset(Topic, Partition, sarama.OffsetOldest, 0).
			SetOffset(Topic, Partition, sarama.OffsetNewest, 1),
		"FetchRequest": sarama.NewMockFetchResponse(t, 1).
			SetMessage(Topic, Partition, 0, sarama.StringEncoder(Message)).
			SetHighWaterMark(Topic, Partition, 1),
	})
	return b
}

// Config returns a config that works with the broker. Sync producers need
// successes, and the contrib matches async successes to spans through message
// headers, which need at least V0_11_0_0.
func Config() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V0_11_0_0
	cfg.Producer.Return.Successes = true
	cfg.Producer.Retry.Backoff = 0
	cfg.Consumer.Retry.Backoff = 0
	return cfg
}

// NewMessage returns a message for Topic.
func NewMessage() *sarama.ProducerMessage {
	return &sarama.ProducerMessage{
		Topic:     Topic,
		Partition: Partition,
		Value:     sarama.StringEncoder(Message),
	}
}

// SendSync sends one message and closes p.
func SendSync(p sarama.SyncProducer) error {
	_, _, err := p.SendMessage(NewMessage())
	return errors.Join(err, p.Close())
}

// SendAsync sends one message, waits for its success, and closes p.
func SendAsync(p sarama.AsyncProducer) error {
	p.Input() <- NewMessage()
	var err error
	select {
	case <-p.Successes():
	case perr := <-p.Errors():
		err = perr
	case <-time.After(timeout):
		err = errors.New("timed out waiting for the message to be sent")
	}
	return errors.Join(err, p.Close())
}

// Consume reads Message from Partition and closes c. It returns once the
// messages channel is closed, which is when the contrib has finished the last
// consumer span.
func Consume(c sarama.Consumer) (err error) {
	defer func() { err = errors.Join(err, c.Close()) }()

	pc, err := c.ConsumePartition(Topic, Partition, sarama.OffsetOldest)
	if err != nil {
		return err
	}
	select {
	case msg := <-pc.Messages():
		if got := string(msg.Value); got != Message {
			err = fmt.Errorf("got message %q, want %q", got, Message)
		}
	case <-time.After(timeout):
		err = errors.New("timed out waiting for the message")
	}
	err = errors.Join(err, pc.Close())
	for range pc.Messages() {
	}
	return err
}
