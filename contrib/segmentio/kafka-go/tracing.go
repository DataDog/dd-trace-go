// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/DataDog/dd-trace-go/contrib/segmentio/kafka-go/v2/internal/tracing"
	"github.com/segmentio/kafka-go"
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

func (w *wMessage) GetValue() []byte {
	return w.Value
}

func (w *wMessage) GetKey() []byte {
	return w.Key
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

func (w *wMessage) GetTopic() string {
	return w.Topic
}

func (w *wMessage) GetPartition() int {
	return w.Partition
}

func (w *wMessage) GetOffset() int64 {
	return w.Offset
}

type wHeader struct {
	kafka.Header
}

func wrapHeader(h kafka.Header) tracing.Header {
	return &wHeader{h}
}

func (w wHeader) GetKey() string {
	return w.Key
}

func (w wHeader) GetValue() []byte {
	return w.Value
}

type wWriter struct {
	*kafka.Writer
}

func (w *wWriter) GetTopic() string {
	return w.Topic
}

func wrapTracingWriter(w *kafka.Writer) tracing.Writer {
	return &wWriter{w}
}
