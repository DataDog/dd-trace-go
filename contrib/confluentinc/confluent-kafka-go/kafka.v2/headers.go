// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/internal/tracing"
)

// A MessageCarrier injects and extracts traces from a kafka.Message.
type MessageCarrier = tracing.MessageCarrier

// NewMessageCarrier creates a new MessageCarrier.
func NewMessageCarrier(msg *kafka.Message) MessageCarrier {
	return tracing.NewMessageCarrier(wrapMessage(msg))
}
