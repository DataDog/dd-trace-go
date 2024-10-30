// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

// A MessageCarrier implements TextMapReader/TextMapWriter for extracting/injecting traces on a kafka.msg
type MessageCarrier struct {
	msg Message
}

var _ interface {
	tracer.TextMapReader
	tracer.TextMapWriter
} = (*MessageCarrier)(nil)

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
