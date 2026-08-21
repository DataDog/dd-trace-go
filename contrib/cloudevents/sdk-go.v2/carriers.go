// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package cloudevents

import (
	"github.com/cloudevents/sdk-go/v2/binding"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/cloudevents/sdk-go/v2/extensions"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// CloudEvent extensions are transport-independent metadata fields. Their names
// cannot contain the punctuation used by Datadog propagation headers, whereas
// the W3C propagation names are valid.
var propagationKeys = [...]string{
	extensions.TraceParentExtension,
	extensions.TraceStateExtension,
	tracer.DefaultBaggageHeader,
}

// eventCarrier adapts a CloudEvent writer to tracer.TextMapWriter.
type eventCarrier struct {
	writer event.EventWriter
}

// Set writes a supported W3C propagation field into the event envelope.
func (c eventCarrier) Set(key, value string) {
	for _, allowed := range propagationKeys {
		if key == allowed {
			c.writer.SetExtension(key, value)
			return
		}
	}
}

// messageCarrier exposes event-envelope metadata to the Datadog propagator.
type messageCarrier struct {
	reader binding.MessageMetadataReader
}

// ForeachKey visits each supported, non-empty W3C propagation field.
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
