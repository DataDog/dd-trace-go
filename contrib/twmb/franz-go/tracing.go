// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"github.com/DataDog/_personal/mentorship/dd-trace-go/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/internal/tracing"
	"github.com/segmentio/kafka-go"
	kgo "github.com/twmb/franz-go/pkg/kgo"
)

type wRecord struct {
	*kgo.Record
}

func wrapRecord(r *kgo.Record) tracing.Record {
	if r == nil {
		return nil
	}
	return &wRecord{r}
}

func (w *wRecord) GetKey() []byte {
	return w.Key
}

func (w *wRecord) GetValue() []byte {
	return w.Value
}

func (w *wRecord) GetHeaders() []tracing.Header {
	hs := make([]tracing.Header, 0, len(w.Headers))
	for _, h := range w.Headers {
		hs = append(hs, wrapHeader(h))
	}
	return hs
}

func (w *wRecord) SetHeaders(headers []tracing.Header) {
	hs := make([]kafka.Header, 0, len(headers))
	for _, h := range headers {
		hs = append(hs, kafka.Header{
			Key:   h.GetKey(),
			Value: h.GetValue(),
		})
	}
	w.Headers = hs
}

func (w *wRecord) GetTopic() string {
	return w.Topic
}

func (w *wRecord) GetPartition() int32 {
	return w.Partition
}

func (w *wRecord) GetOffset() int64 {
	return w.Offset
}

type wHeader struct {
	kgo.RecordHeader
}

func wrapHeader(h kgo.RecordHeader) tracing.Header {
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

func ExtractSpanContext(r *kgo.Record) (*tracer.SpanContext, error) {
	return tracing.ExtractSpanContext(wrapRecord(r))
}
