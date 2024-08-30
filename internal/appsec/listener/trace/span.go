// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"encoding/json"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// OnServiceEntryStart is a listener the trace.ServiceEntrySpanOperation start event.
// It listens for tags and serializable tags and sets them on the span when finishing the operation.
func OnServiceEntryStart(op *trace.ServiceEntrySpanOperation, _ trace.ServiceEntrySpanArgs) {
	var (
		tags     = make(map[string]any, 5)
		jsonTags = make(map[string]any, 1)
		mu       sync.RWMutex
	)

	dyngo.OnData(op, func(tag trace.ServiceEntrySpanTag) {
		mu.Lock()
		defer mu.Unlock()
		tags[tag.Key] = tag.Value
	})

	dyngo.OnData(op, func(tag trace.SerializableServiceEntrySpanTag) {
		mu.Lock()
		defer mu.Unlock()
		jsonTags[tag.Key] = tag.Value
	})

	dyngo.OnFinish(op, func(_ *trace.ServiceEntrySpanOperation, res trace.ServiceEntrySpanRes) {
		mu.Lock()
		defer mu.Unlock()

		for k, v := range tags {
			res.Span.SetTag(k, v)
		}

		for k, v := range jsonTags {
			strValue, err := json.Marshal(v)
			if err != nil {
				log.Debug("appsec: failed to marshal tag %s: %v", k, err)
			}
			res.Span.SetTag(k, strValue)
		}
	})
}

func OnSpanStart(op *trace.SpanOperation, _ trace.SpanArgs) {
	var (
		tags = make(map[string]any)
		mu   sync.RWMutex
	)

	dyngo.OnData(op, func(tag trace.SpanTag) {
		mu.Lock()
		defer mu.Unlock()
		tags[tag.Key] = tag.Value
	})

	dyngo.OnFinish(op, func(_ *trace.SpanOperation, res trace.SpanRes) {
		mu.Lock()
		defer mu.Unlock()

		for k, v := range tags {
			res.Span.SetTag(k, v)
		}
	})
}
