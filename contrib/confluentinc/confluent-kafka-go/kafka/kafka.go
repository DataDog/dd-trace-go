// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kafka provides functions to trace the confluentinc/confluent-kafka-go package (https://github.com/confluentinc/confluent-kafka-go).
package kafka // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka/v2"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

// NewConsumer calls kafka.NewConsumer and wraps the resulting Consumer.
func NewConsumer(conf *kafka.ConfigMap, opts ...Option) (*Consumer, error) {
	return v2.NewConsumer(conf, opts...)
}

// NewProducer calls kafka.NewProducer and wraps the resulting Producer.
func NewProducer(conf *kafka.ConfigMap, opts ...Option) (*Producer, error) {
	return v2.NewProducer(conf, opts...)
}

// A Consumer wraps a kafka.Consumer.
type Consumer = v2.Consumer

// WrapConsumer wraps a kafka.Consumer so that any consumed events are traced.
func WrapConsumer(c *kafka.Consumer, opts ...Option) *Consumer {
	return v2.WrapConsumer(c, opts...)
}

// A Producer wraps a kafka.Producer.
type Producer = v2.Producer

// WrapProducer wraps a kafka.Producer so requests are traced.
func WrapProducer(p *kafka.Producer, opts ...Option) *Producer {
	return v2.WrapProducer(p, opts...)
}
