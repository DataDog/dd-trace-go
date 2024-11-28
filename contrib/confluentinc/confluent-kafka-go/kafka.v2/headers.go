// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/confluentinc/confluent-kafka-go/kafka.v2/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// A MessageCarrier injects and extracts traces from a kafka.Message.
type MessageCarrier = v2.MessageCarrier

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*MessageCarrier)(nil)

// NewMessageCarrier creates a new MessageCarrier.
func NewMessageCarrier(msg *kafka.Message) MessageCarrier {
	return v2.NewMessageCarrier(msg)
}
