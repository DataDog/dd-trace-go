// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo

import (
	"context"

	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/twmb/franz-go/pkg/kgo"
)

// record wraps kgo.Record to implement the tracing.Record interface.
// This adapter decouples the internal/tracing package from kgo,
// which is required to avoid cyclic imports when supporting orchestrion.
type record struct {
	*kgo.Record
}

func wrapRecord(r *kgo.Record) tracing.Record {
	if r == nil {
		return nil
	}
	return &record{r}
}

func (w *record) GetKey() []byte {
	return w.Key
}

func (w *record) GetValue() []byte {
	return w.Value
}

func (w *record) GetHeaders() []tracing.Header {
	hs := make([]tracing.Header, 0, len(w.Headers))
	for _, h := range w.Headers {
		hs = append(hs, wrapHeader(h))
	}
	return hs
}

func (w *record) SetHeaders(headers []tracing.Header) {
	hs := make([]kgo.RecordHeader, 0, len(headers))
	for _, h := range headers {
		hs = append(hs, kgo.RecordHeader{
			Key:   h.GetKey(),
			Value: h.GetValue(),
		})
	}
	w.Headers = hs
}

func (w *record) GetTopic() string {
	return w.Topic
}

func (w *record) GetPartition() int32 {
	return w.Partition
}

func (w *record) GetOffset() int64 {
	return w.Offset
}

func (w *record) GetContext() context.Context {
	return w.Context
}

type header struct {
	kgo.RecordHeader
}

func wrapHeader(h kgo.RecordHeader) tracing.Header {
	return &header{h}
}

func (w header) GetKey() string {
	return w.Key
}

func (w header) GetValue() []byte {
	return w.Value
}

func ExtractSpanContext(r *kgo.Record) (*tracer.SpanContext, error) {
	return tracing.ExtractSpanContext(wrapRecord(r))
}
