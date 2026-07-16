// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"context"
	"strings"
)

const (
	headerKeyDatadogTags     = "x-datadog-tags"
	tagKeyLLMObsPAgentSpanID = "_dd.p.llmobs_pagent_span_id"
	tagKeyLLMObsPAgentName   = "_dd.p.llmobs_pagent_name"

	// maxDatadogTagsLen bounds the total x-datadog-tags value length. The name
	// tag is only appended when the result stays within this budget.
	maxDatadogTagsLen = 512
)

// InjectContext writes the active span's outbound agent attribution into the
// carrier's x-datadog-tags value. Exported so the public llmobs package (which
// cannot read Span's unexported fields) can delegate here.
func InjectContext(ctx context.Context, carrier map[string]string) {
	span, ok := ActiveLLMSpanFromContext(ctx)
	if !ok || span == nil {
		return
	}
	injectAgentAttribution(span, carrier)
}

// ExtractContext reads agent attribution from the carrier and attaches it to a
// PropagatedLLMSpan on the returned context, preserving any fields already set.
func ExtractContext(ctx context.Context, carrier map[string]string) context.Context {
	name, spanID, present := extractAgentAttribution(carrier)
	if !present {
		return ctx
	}

	// Update, do not overwrite: preserve any existing MLApp/TraceID/SpanID.
	var prop PropagatedLLMSpan
	if existing, ok := PropagatedLLMSpanFromContext(ctx); ok && existing != nil {
		prop = *existing
	}
	prop.ParentAgentName = name
	prop.ParentAgentSpanID = spanID
	return ContextWithPropagatedLLMSpan(ctx, &prop)
}

// injectAgentAttribution resolves the span's outbound agent attribution and
// merges the resulting tags into the carrier's x-datadog-tags value.
//
//	active span is an agent -> (self.name, self.SpanID())
//	otherwise               -> (span.parentAgentName, span.parentAgentSpanID)
func injectAgentAttribution(span *Span, carrier map[string]string) {
	var name, spanID string
	if span.spanKind == SpanKindAgent {
		name, spanID = span.name, span.SpanID()
	} else {
		name, spanID = span.parentAgentName, span.parentAgentSpanID
	}
	if spanID == "" {
		return // no agent ancestor: write nothing
	}

	existing := carrier[headerKeyDatadogTags]
	parts := make([]string, 0, 2)
	if existing != "" {
		parts = append(parts, existing)
	}
	parts = append(parts, tagKeyLLMObsPAgentSpanID+"="+spanID)

	// Append the name only if wire-safe and within the total length budget.
	if name != "" && agentNameWireSafe(name) {
		candidate := append(parts, tagKeyLLMObsPAgentName+"="+name)
		if joined := strings.Join(candidate, ","); len(joined) <= maxDatadogTagsLen {
			parts = candidate
		}
	}
	carrier[headerKeyDatadogTags] = strings.Join(parts, ",")
}

// extractAgentAttribution parses the carrier's x-datadog-tags value for the
// llmobs agent attribution tags. present is true if the span id tag was found.
func extractAgentAttribution(carrier map[string]string) (name string, spanID string, present bool) {
	header, ok := carrier[headerKeyDatadogTags]
	if !ok || header == "" {
		return "", "", false
	}
	for _, pair := range strings.Split(header, ",") {
		k, v, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		switch k {
		case tagKeyLLMObsPAgentSpanID:
			spanID = v
			present = true
		case tagKeyLLMObsPAgentName:
			name = v
		}
	}
	return name, spanID, present
}
