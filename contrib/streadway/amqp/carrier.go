// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package amqp

import (
	"github.com/streadway/amqp"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type TableCarrier amqp.Table

var (
	_ tracer.TextMapWriter           = TableCarrier(nil)
	_ tracer.TextMapReader           = TableCarrier(nil)
	_ datastreams.TextMapReaderByKey = TableCarrier(nil)
)

func (c TableCarrier) Set(key, val string) {
	c[key] = val
}

func (c TableCarrier) Get(key string) (string, bool) {
	return headerString(c[key])
}

func (c TableCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		s, ok := headerString(v)
		if !ok {
			continue
		}
		if err := handler(k, s); err != nil {
			return err
		}
	}
	return nil
}

func headerString(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	default:
		return "", false
	}
}
