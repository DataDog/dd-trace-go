// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

type Message interface {
	GetValue() []byte
	GetKey() []byte
	GetHeaders() []Header
	SetHeaders([]Header)
	GetTopicPartition() TopicPartition
	Unwrap() any
}

type Header interface {
	GetKey() string
	GetValue() []byte
}

type KafkaHeader struct {
	Key   string
	Value []byte
}

func (h KafkaHeader) GetKey() string {
	return h.Key
}

func (h KafkaHeader) GetValue() []byte {
	return h.Value
}

type OffsetsCommitted interface {
	GetError() error
	GetOffsets() []TopicPartition
}

type TopicPartition interface {
	GetTopic() string
	GetPartition() int32
	GetOffset() int64
	GetError() error
}

type Event interface {
	KafkaMessage() (Message, bool)
	KafkaOffsetsCommitted() (OffsetsCommitted, bool)
}

type Consumer interface {
	GetWatermarkOffsets(topic string, partition int32) (low int64, high int64, err error)
}

type ConfigMap interface {
	Get(key string, defval any) (any, error)
}

type SpanStore struct {
	Prev ddtrace.Span
}
