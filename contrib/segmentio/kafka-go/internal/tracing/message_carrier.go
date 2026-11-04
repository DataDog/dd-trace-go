// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// A MessageCarrier implements TextMapReader/TextMapWriter for extracting/injecting traces on a kafka.Message
type MessageCarrier struct {
	msg Message
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*MessageCarrier)(nil)

var _ datastreams.TextMapReaderByKey = (*MessageCarrier)(nil)

// ForeachKey conforms to the TextMapReader interface.
func (c MessageCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, h := range c.msg.GetHeaders() {
		err := handler(h.GetKey(), string(h.GetValue()))
		if err != nil {
			return err
		}
	}
	return nil
}

// Get implements datastreams.TextMapReaderByKey, converting only the matched
// header's value to a string (unlike ForeachKey, which converts them all). When
// a key appears more than once it returns the last occurrence, matching
// ForeachKey's last-wins behavior for duplicate headers.
func (c MessageCarrier) Get(key string) (string, bool) {
	var val []byte
	found := false
	for _, h := range c.msg.GetHeaders() {
		if h.GetKey() == key {
			val = h.GetValue()
			found = true
		}
	}
	if !found {
		return "", false
	}
	return string(val), true
}

// Set implements TextMapWriter
func (c MessageCarrier) Set(key, val string) {
	headers := c.msg.GetHeaders()
	// ensure uniqueness of keys
	for i := 0; i < len(headers); i++ {
		if headers[i].GetKey() == key {
			headers = append(headers[:i], headers[i+1:]...)
			i--
		}
	}
	headers = append(headers, KafkaHeader{
		Key:   key,
		Value: []byte(val),
	})
	c.msg.SetHeaders(headers)
}

func NewMessageCarrier(msg Message) MessageCarrier {
	return MessageCarrier{msg: msg}
}
