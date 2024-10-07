// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/confluentinc/confluent-kafka-go/kafka"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/internal/tracing"
)

type wMessage struct {
	*kafka.Message
}

func wrapMessage(msg *kafka.Message) tracing.Message {
	if msg == nil {
		return nil
	}
	return &wMessage{msg}
}

func (w *wMessage) Unwrap() any {
	return w.Message
}

func (w *wMessage) GetValue() []byte {
	return w.Message.Value
}

func (w *wMessage) GetKey() []byte {
	return w.Message.Key
}

func (w *wMessage) GetHeaders() []tracing.Header {
	hs := make([]tracing.Header, 0, len(w.Headers))
	for _, h := range w.Headers {
		hs = append(hs, wrapHeader(h))
	}
	return hs
}

func (w *wMessage) SetHeaders(headers []tracing.Header) {
	hs := make([]kafka.Header, 0, len(headers))
	for _, h := range headers {
		hs = append(hs, kafka.Header{
			Key:   h.GetKey(),
			Value: h.GetValue(),
		})
	}
	w.Message.Headers = hs
}

func (w *wMessage) GetTopicPartition() tracing.TopicPartition {
	return wrapTopicPartition(w.Message.TopicPartition)
}

type wHeader struct {
	kafka.Header
}

func wrapHeader(h kafka.Header) tracing.Header {
	return &wHeader{h}
}

func (w wHeader) GetKey() string {
	return w.Header.Key
}

func (w wHeader) GetValue() []byte {
	return w.Header.Value
}

type wTopicPartition struct {
	kafka.TopicPartition
}

func wrapTopicPartition(tp kafka.TopicPartition) tracing.TopicPartition {
	return wTopicPartition{tp}
}

func wrapTopicPartitions(tps []kafka.TopicPartition) []tracing.TopicPartition {
	wtps := make([]tracing.TopicPartition, 0, len(tps))
	for _, tp := range tps {
		wtps = append(wtps, wTopicPartition{tp})
	}
	return wtps
}

func (w wTopicPartition) GetTopic() string {
	if w.Topic == nil {
		return ""
	}
	return *w.Topic
}

func (w wTopicPartition) GetPartition() int32 {
	return w.Partition
}

func (w wTopicPartition) GetOffset() int64 {
	return int64(w.Offset)
}

func (w wTopicPartition) GetError() error {
	return w.Error
}

type wEvent struct {
	kafka.Event
}

func wrapEvent(event kafka.Event) tracing.Event {
	return wEvent{event}
}

func (w wEvent) KafkaMessage() (tracing.Message, bool) {
	if m, ok := w.Event.(*kafka.Message); ok {
		return wrapMessage(m), true
	}
	return nil, false
}

func (w wEvent) KafkaOffsetsCommitted() (tracing.OffsetsCommitted, bool) {
	if oc, ok := w.Event.(kafka.OffsetsCommitted); ok {
		return wrapOffsetsCommitted(oc), true
	}
	return nil, false
}

type wOffsetsCommitted struct {
	kafka.OffsetsCommitted
}

func wrapOffsetsCommitted(oc kafka.OffsetsCommitted) tracing.OffsetsCommitted {
	return wOffsetsCommitted{oc}
}

func (w wOffsetsCommitted) GetError() error {
	return w.Error
}

func (w wOffsetsCommitted) GetOffsets() []tracing.TopicPartition {
	ttps := make([]tracing.TopicPartition, 0, len(w.Offsets))
	for _, tp := range w.Offsets {
		ttps = append(ttps, wrapTopicPartition(tp))
	}
	return ttps
}

type wConfigMap struct {
	cfg *kafka.ConfigMap
}

func wrapConfigMap(cm *kafka.ConfigMap) tracing.ConfigMap {
	return &wConfigMap{cm}
}

func (w *wConfigMap) Get(key string, defVal any) (any, error) {
	return w.cfg.Get(key, defVal)
}
