// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/IBM/sarama"
)

// A ProducerMessageCarrier injects and extracts traces from a sarama.ProducerMessage.
type ProducerMessageCarrier = v2.ProducerMessageCarrier

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*ProducerMessageCarrier)(nil)

// NewProducerMessageCarrier creates a new ProducerMessageCarrier.
func NewProducerMessageCarrier(msg *sarama.ProducerMessage) ProducerMessageCarrier {
	return v2.NewProducerMessageCarrier(msg)
}

// A ConsumerMessageCarrier injects and extracts traces from a sarama.ConsumerMessage.
type ConsumerMessageCarrier = v2.ConsumerMessageCarrier

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*ConsumerMessageCarrier)(nil)

// NewConsumerMessageCarrier creates a new ConsumerMessageCarrier.
func NewConsumerMessageCarrier(msg *sarama.ConsumerMessage) ConsumerMessageCarrier {
	return v2.NewConsumerMessageCarrier(msg)
}
