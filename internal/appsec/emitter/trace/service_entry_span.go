// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"context"
	"encoding/json"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

type (
	// ServiceEntrySpanOperation is a dyngo.Operation that holds a the first span of a service. Usually a http or grpc span.
	ServiceEntrySpanOperation struct {
		dyngo.Operation
		tags     map[string]any
		jsonTags map[string]any
		mu       sync.Mutex
	}

	// ServiceEntrySpanArgs is the arguments for a ServiceEntrySpanOperation
	ServiceEntrySpanArgs struct{}

	// ServiceEntrySpanTag is a key value pair event that is used to tag a service entry span
	ServiceEntrySpanTag struct {
		Key   string
		Value any
	}

	// JSONServiceEntrySpanTag is a key value pair event that is used to tag a service entry span
	// It will be serialized as JSON when added to the span
	JSONServiceEntrySpanTag struct {
		Key   string
		Value any
	}

	// ServiceEntrySpanTagsBulk is a bulk event that is used to send tags to a service entry span
	ServiceEntrySpanTagsBulk struct {
		Tags     []JSONServiceEntrySpanTag
		JSONTags []JSONServiceEntrySpanTag
	}
)

func (ServiceEntrySpanArgs) IsArgOf(*ServiceEntrySpanOperation) {}

// SetTag adds the key/value pair to the tags to add to the service entry span
func (op *ServiceEntrySpanOperation) SetTag(key string, value any) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.tags[key] = value
}

// SetJSONTag adds the key/value pair to the tags to add to the service entry span. Value will be serialized as JSON.
func (op *ServiceEntrySpanOperation) SetJSONTag(key string, value any) {
	op.mu.Lock()
	defer op.mu.Unlock()
	op.jsonTags[key] = value
}

// SetJSONTags adds the key/value pairs to the tags to add to the service entry span. Values will be serialized as JSON.
func (op *ServiceEntrySpanOperation) SetJSONTags(tags map[string]any) {
	op.mu.Lock()
	defer op.mu.Unlock()
	for k, v := range tags {
		op.jsonTags[k] = v
	}
}

// SetTags fills the span tags using the key/value pairs found in `tags`
func (op *ServiceEntrySpanOperation) SetTags(tags map[string]any) {
	op.mu.Lock()
	defer op.mu.Unlock()
	for k, v := range tags {
		op.tags[k] = v
	}
}

// SetStringTags fills the span tags using the key/value pairs found in `tags`
func (op *ServiceEntrySpanOperation) SetStringTags(tags map[string]string) {
	op.mu.Lock()
	defer op.mu.Unlock()
	for k, v := range tags {
		op.tags[k] = v
	}
}

// OnServiceEntrySpanTagEvent is a callback that is called when a dyngo.OnData is triggered with a ServiceEntrySpanTag event
func (op *ServiceEntrySpanOperation) OnServiceEntrySpanTagEvent(tag ServiceEntrySpanTag) {
	op.SetTag(tag.Key, tag.Value)
}

// OnJSONServiceEntrySpanTagEvent is a callback that is called when a dyngo.OnData is triggered with a JSONServiceEntrySpanTag event
func (op *ServiceEntrySpanOperation) OnJSONServiceEntrySpanTagEvent(tag JSONServiceEntrySpanTag) {
	op.SetJSONTag(tag.Key, tag.Value)
}

// OnServiceEntrySpanTagsBulkEvent is a callback that is called when a dyngo.OnData is triggered with a ServiceEntrySpanTagsBulk event
func (op *ServiceEntrySpanOperation) OnServiceEntrySpanTagsBulkEvent(bulk ServiceEntrySpanTagsBulk) {
	for _, v := range bulk.Tags {
		op.SetTag(v.Key, v.Value)
	}

	for _, v := range bulk.JSONTags {
		op.SetJSONTag(v.Key, v.Value)
	}
}

// OnSpanTagEvent is a listener for SpanTag events.
func (op *ServiceEntrySpanOperation) OnSpanTagEvent(tag SpanTag) {
	op.SetTag(tag.Key, tag.Value)
}

func StartServiceEntrySpanOperation(ctx context.Context) (*ServiceEntrySpanOperation, context.Context) {
	parent, _ := dyngo.FromContext(ctx)
	op := &ServiceEntrySpanOperation{
		Operation: dyngo.NewOperation(parent),
		tags:      make(map[string]any),
		jsonTags:  make(map[string]any),
	}
	return op, dyngo.StartAndRegisterOperation(ctx, op, ServiceEntrySpanArgs{})
}

func (op *ServiceEntrySpanOperation) Finish(span TagSetter) {
	if _, ok := span.(*NoopTagSetter); ok { // If the span is a NoopTagSetter or is nil, we don't need to set any tags
		return
	}

	op.mu.Lock()
	defer op.mu.Unlock()

	for k, v := range op.tags {
		span.SetTag(k, v)
	}

	for k, v := range op.jsonTags {
		strValue, err := json.Marshal(v)
		if err != nil {
			log.Debug("appsec: failed to marshal tag %s: %v", k, err)
		}
		span.SetTag(k, string(strValue))
	}
}
