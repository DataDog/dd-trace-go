// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package trace provides functions to annotate trace spans with AppSec related
// information.
package trace

import (
	"encoding/json"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
)

// BlockedRequestTag used to convey whether a request is blocked
const BlockedRequestTag = "appsec.blocked"

// TagSetter is the interface needed to set a span tag.
type TagSetter interface {
	SetTag(string, any)
}

// NoopTagSetter is a TagSetter that does nothing. Useful when no tracer
// Span is available, but a TagSetter is assumed.
type NoopTagSetter struct{}

func (NoopTagSetter) SetTag(string, any) {
	// Do nothing
}

// SetAppSecEnabledTags sets the AppSec-specific span tags that are expected to
// be in the web service entry span (span of type `web`) when AppSec is enabled.
func SetAppSecEnabledTags(span TagSetter) {
	span.SetTag("_dd.appsec.enabled", 1)
	span.SetTag("_dd.runtime_family", "go")
}

// SetEventSpanTags sets the security event span tags into the service entry span.
func SetEventSpanTags(span TagSetter, events []any) error {
	if len(events) == 0 {
		return nil
	}

	// Set the appsec event span tag
	val, err := makeEventTagValue(events)
	if err != nil {
		return err
	}
	span.SetTag("_dd.appsec.json", string(val))
	// Keep this span due to the security event
	//
	// This is a workaround to tell the tracer that the trace was kept by AppSec.
	// Passing any other value than `appsec.SamplerAppSec` has no effect.
	// Customers should use `span.SetTag(ext.ManualKeep, true)` pattern
	// to keep the trace, manually.
	span.SetTag(ext.ManualKeep, samplernames.AppSec)
	span.SetTag("_dd.origin", "appsec")
	// Set the appsec.event tag needed by the appsec backend
	span.SetTag("appsec.event", true)
	return nil
}

// SetTags fills the span tags using the key/value pairs found in `tags`
func SetTags[V any](span TagSetter, tags map[string]V) {
	for k, v := range tags {
		span.SetTag(k, v)
	}
}

// Create the value of the security event tag.
func makeEventTagValue(events []any) (json.RawMessage, error) {
	type eventTagValue struct {
		Triggers []any `json:"triggers"`
	}

	tag, err := json.Marshal(eventTagValue{events})
	if err != nil {
		return nil, fmt.Errorf("unexpected error while serializing the appsec event span tag: %v", err)
	}

	return tag, nil
}
