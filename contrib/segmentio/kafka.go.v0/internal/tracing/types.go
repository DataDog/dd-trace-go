// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

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

type Writer interface {
	GetTopic() string
}

type Message interface {
	GetValue() []byte
	GetKey() []byte
	GetHeaders() []Header
	SetHeaders([]Header)
	GetTopic() string
	GetPartition() int
	GetOffset() int64
}

// KafkaConfig holds information from the kafka config for span tags.
type KafkaConfig struct {
	BootstrapServers string
	ConsumerGroupID  string
}
