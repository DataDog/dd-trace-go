// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package kgo

import (
	kgo "github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// kafkaHeadersCarrier implements tracer.TextMapWriter and tracer.TextMapReader
// over a kgo.Record's headers for span context propagation.
type kafkaHeadersCarrier struct {
	record *kgo.Record
}

var _ interface {
	tracer.TextMapWriter
	tracer.TextMapReader
} = (*kafkaHeadersCarrier)(nil)

func newKafkaHeadersCarrier(r *kgo.Record) *kafkaHeadersCarrier {
	return &kafkaHeadersCarrier{record: r}
}

// ForeachKey implements tracer.TextMapReader.
func (c kafkaHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.record.Headers {
		if err := handler(h.Key, string(h.Value)); err != nil {
			return err
		}
	}
	return nil
}

// Set implements tracer.TextMapWriter.
func (c *kafkaHeadersCarrier) Set(key, val string) {
	for i, h := range c.record.Headers {
		if h.Key == key {
			c.record.Headers[i].Value = []byte(val)
			return
		}
	}
	c.record.Headers = append(c.record.Headers, kgo.RecordHeader{Key: key, Value: []byte(val)})
}

// ExtractSpanContext extracts the span context from a record's headers.
func ExtractSpanContext(r *kgo.Record) (*tracer.SpanContext, error) {
	return tracer.Extract(newKafkaHeadersCarrier(r))
}
