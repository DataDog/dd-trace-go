// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

// Header abstracts access to Kafka message headers. This interface allows
// the tracing package to read/write headers without depending on franz-go types,
// enabling the KafkaHeadersCarrier to inject/extract span context.
type Header interface {
	GetKey() string
	GetValue() []byte
}

// KafkaHeader is a concrete implementation of Header used by KafkaHeadersCarrier
// when setting headers on a record during span context injection.
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

// Record abstracts a Kafka record for tracing purposes. This interface decouples
// the internal tracing logic from franz-go's kgo.Record type, allowing the tracing
// package to be tested independently.
type Record interface {
	GetValue() []byte
	GetKey() []byte
	GetHeaders() []Header
	SetHeaders([]Header)
	GetTopic() string
	GetPartition() int32
	GetOffset() int64
}
