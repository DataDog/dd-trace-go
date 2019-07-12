// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package sarama

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	sarama "gopkg.in/Shopify/sarama.v1"
)

// A ProducerMessageCarrier injects and extracts traces from a sarama.ProducerMessage.
type ProducerMessageCarrier struct {
	msg *sarama.ProducerMessage
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*ProducerMessageCarrier)(nil)

// ForeachKey iterates over every header.
func (c ProducerMessageCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.msg.Headers {
		err := handler(string(h.Key), string(h.Value))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set sets a header.
func (c ProducerMessageCarrier) Set(key, val string) {
	// ensure uniqueness of keys
	for i := 0; i < len(c.msg.Headers); i++ {
		if string(c.msg.Headers[i].Key) == key {
			c.msg.Headers = append(c.msg.Headers[:i], c.msg.Headers[i+1:]...)
			i--
		}
	}
	c.msg.Headers = append(c.msg.Headers, sarama.RecordHeader{
		Key:   []byte(key),
		Value: []byte(val),
	})
}

// NewProducerMessageCarrier creates a new ProducerMessageCarrier.
func NewProducerMessageCarrier(msg *sarama.ProducerMessage) ProducerMessageCarrier {
	return ProducerMessageCarrier{msg}
}

// A ConsumerMessageCarrier injects and extracts traces from a sarama.ConsumerMessage.
type ConsumerMessageCarrier struct {
	msg *sarama.ConsumerMessage
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*ConsumerMessageCarrier)(nil)

// NewConsumerMessageCarrier creates a new ConsumerMessageCarrier.
func NewConsumerMessageCarrier(msg *sarama.ConsumerMessage) ConsumerMessageCarrier {
	return ConsumerMessageCarrier{msg}
}

// ForeachKey iterates over every header.
func (c ConsumerMessageCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.msg.Headers {
		if h != nil {
			err := handler(string(h.Key), string(h.Value))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Set sets a header.
func (c ConsumerMessageCarrier) Set(key, val string) {
	// ensure uniqueness of keys
	for i := 0; i < len(c.msg.Headers); i++ {
		if c.msg.Headers[i] != nil && string(c.msg.Headers[i].Key) == key {
			c.msg.Headers = append(c.msg.Headers[:i], c.msg.Headers[i+1:]...)
			i--
		}
	}
	c.msg.Headers = append(c.msg.Headers, &sarama.RecordHeader{
		Key:   []byte(key),
		Value: []byte(val),
	})
}
