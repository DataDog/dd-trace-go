// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import (
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/event"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// CloudEvents extension names cannot contain the punctuation used by Datadog
// propagation headers. W3C propagation keys are valid CloudEvents extensions.
var propagationKeys = [...]string{"traceparent", "tracestate", "baggage"}

type eventCarrier struct {
	writer event.EventWriter
}

func (c eventCarrier) Set(key, value string) {
	for _, allowed := range propagationKeys {
		if key == allowed {
			c.writer.SetExtension(key, value)
			return
		}
	}
}

type messageCarrier struct {
	reader binding.MessageMetadataReader
}

func (c messageCarrier) ForeachKey(fn func(string, string) error) error {
	for _, key := range propagationKeys {
		if value, ok := c.reader.GetExtension(key).(string); ok && value != "" {
			if err := fn(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

var _ tracer.TextMapWriter = eventCarrier{}
var _ tracer.TextMapReader = messageCarrier{}
