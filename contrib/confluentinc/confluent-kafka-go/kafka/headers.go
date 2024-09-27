// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka/internal/tracing"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

type MessageCarrier = tracing.MessageCarrier

func NewMessageCarrier(msg *kafka.Message) MessageCarrier {
	return tracing.NewMessageCarrier(msg)
}
