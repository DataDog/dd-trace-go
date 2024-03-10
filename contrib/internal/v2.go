// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace"

func BuildStartSpanConfigV2(opts ...ddtrace.StartSpanOption) *v2.StartSpanConfig {
	ssc := new(ddtrace.StartSpanConfig)
	for _, o := range opts {
		o(ssc)
	}
	var parent *v2.SpanContext
	if ssc.Parent != nil {
		parent = resolveSpantContextV2(ssc.Parent)
	}
	return &v2.StartSpanConfig{
		Context:   ssc.Context,
		Parent:    parent,
		SpanID:    ssc.SpanID,
		SpanLinks: ssc.SpanLinks,
		StartTime: ssc.StartTime,
		Tags:      ssc.Tags,
	}
}

func resolveSpantContextV2(ctx ddtrace.SpanContext) *v2.SpanContext {
	if parent, ok := ctx.(SpanContextV2Adapter); ok {
		return parent.Ctx
	}

	// We may have an otelToDDSpanContext that can be converted to a v2.SpanContext
	// by copying its fields.
	// Other SpanContext may fall through here, but they are not guaranteed to be
	// fully supported, as the resulting v2.SpanContext may be missing data.
	return v2.FromGenericCtx(&SpanContextV1Adapter{Ctx: ctx})
}
