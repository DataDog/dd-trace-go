// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package franz

import (
	"github.com/twmb/franz-go/pkg/kgo"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// A RecordHeadersCarrier injects and extracts traces from kgo.Record headers.
type RecordHeadersCarrier struct {
	headers []kgo.RecordHeader
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*RecordHeadersCarrier)(nil)

// ForeachKey iterates over every header.
func (c RecordHeadersCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.headers {
		err := handler(string(h.Key), string(h.Value))
		if err != nil {
			return err
		}
	}
	return nil
}

// Set sets a header.
func (c *RecordHeadersCarrier) Set(key, val string) {
	// ensure uniqueness of keys
	for i := 0; i < len(c.headers); i++ {
		if c.headers[i].Key == key {
			c.headers = append(c.headers[:i], c.headers[i+1:]...)
			i--
		}
	}
	c.headers = append(c.headers, kgo.RecordHeader{
		Key:   key,
		Value: []byte(val),
	})
}

// NewRecordHeadersCarrier creates a new RecordHeadersCarrier.
func NewRecordHeadersCarrier(headers []kgo.RecordHeader) *RecordHeadersCarrier {
	return &RecordHeadersCarrier{headers: headers}
}

// GetHeaders returns the modified headers.
func (c *RecordHeadersCarrier) GetHeaders() []kgo.RecordHeader {
	return c.headers
}
