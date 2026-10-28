// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kafkav1 creates confluent-kafka-go v1 clients inside a dependency
// module.
package kafkav1

import "github.com/confluentinc/confluent-kafka-go/kafka"

// NewProducer returns a producer for conf.
func NewProducer(conf *kafka.ConfigMap) (*kafka.Producer, error) {
	return kafka.NewProducer(conf)
}

// NewConsumer returns a consumer for conf.
func NewConsumer(conf *kafka.ConfigMap) (*kafka.Consumer, error) {
	return kafka.NewConsumer(conf)
}
