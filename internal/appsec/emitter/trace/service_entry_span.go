// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	// ServiceEntrySpanOperation is a dyngo.Operation that holds a the first span of a service. Usually a http or grpc span.
	ServiceEntrySpanOperation struct {
		dyngo.Operation
		SpanOperation

		JsonTags map[string]any
	}

	// ServiceEntrySpanArgs is the arguments for a ServiceEntrySpanOperation
	ServiceEntrySpanArgs struct{}

	// ServiceEntrySpanRes is the result for a ServiceEntrySpanOperation
	ServiceEntrySpanRes struct {
		SpanRes
	}

	// ServiceEntrySpanTag is a key value pair event that is used to tag a service entry span
	ServiceEntrySpanTag struct {
		Key   string
		Value any
	}

	// JsonServiceEntrySpanTag is a key value pair event that is used to tag a service entry span
	// It will be serialized as JSON when added to the span
	JsonServiceEntrySpanTag struct {
		Key   string
		Value any
	}

	// ServiceEntrySpanTagsBulk is a bulk event that is used to send tags to a service entry span
	ServiceEntrySpanTagsBulk struct {
		Tags     []JsonServiceEntrySpanTag
		JsonTags []JsonServiceEntrySpanTag
	}
)

func (ServiceEntrySpanArgs) IsArgOf(*ServiceEntrySpanOperation)   {}
func (ServiceEntrySpanRes) IsResultOf(*ServiceEntrySpanOperation) {}

// SetTag adds the key/value pair to the tags to add to the service entry span
func (op *ServiceEntrySpanOperation) SetTag(key string, value any) {
	op.Mutex.Lock()
	defer op.Mutex.Unlock()
	op.Tags[key] = value
}

// SetJsonTag adds the key/value pair to the tags to add to the service entry span. Value will be serialized as JSON.
func (op *ServiceEntrySpanOperation) SetJsonTag(key string, value any) {
	op.Mutex.Lock()
	defer op.Mutex.Unlock()
	op.JsonTags[key] = value
}

// SetTags fills the span tags using the key/value pairs found in `tags`
func (op *ServiceEntrySpanOperation) SetTags(tags map[string]any) {
	op.Mutex.Lock()
	defer op.Mutex.Unlock()
	for k, v := range tags {
		op.Tags[k] = v
	}
}

// OnServiceEntrySpanTagEvent is a callback that is called when a dyngo.OnData is triggered with a ServiceEntrySpanTag event
func (op *ServiceEntrySpanOperation) OnServiceEntrySpanTagEvent(tag ServiceEntrySpanTag) {
	op.SetTag(tag.Key, tag.Value)
}

// OnJsonServiceEntrySpanTagEvent is a callback that is called when a dyngo.OnData is triggered with a JsonServiceEntrySpanTag event
func (op *ServiceEntrySpanOperation) OnJsonServiceEntrySpanTagEvent(tag JsonServiceEntrySpanTag) {
	op.SetJsonTag(tag.Key, tag.Value)
}

// OnServiceEntrySpanTagsBulkEvent is a callback that is called when a dyngo.OnData is triggered with a ServiceEntrySpanTagsBulk event
func (op *ServiceEntrySpanOperation) OnServiceEntrySpanTagsBulkEvent(bulk ServiceEntrySpanTagsBulk) {
	for _, v := range bulk.Tags {
		op.SetTag(v.Key, v.Value)
	}

	for _, v := range bulk.JsonTags {
		op.SetJsonTag(v.Key, v.Value)
	}
}

func (op *ServiceEntrySpanOperation) Start(ctx context.Context) context.Context {
	return dyngo.StartAndRegisterOperation(ctx, op, ServiceEntrySpanArgs{})
}

func (op *ServiceEntrySpanOperation) Finish(span ddtrace.Span) {
	dyngo.FinishOperation(op, ServiceEntrySpanRes{SpanRes{span}})
}
