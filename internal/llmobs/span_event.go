// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	// DefaultParentID is the parent ID used for root spans.
	DefaultParentID = "undefined"

	collectionErrorDroppedIO = "dropped_io"
	droppedValueText         = "[This value has been dropped because this span's size exceeds the 1MB size limit.]"

	tagKeySource        = "source"
	tagKeyLanguage      = "language"
	tagKeyTracerVersion = "ddtrace.version"
	tagKeyError         = "error"
	tagKeyErrorType     = "error_type"

	tagValueSource   = "integration"
	tagValueLanguage = "go"

	metaKeyModelName     = "model_name"
	metaKeyModelProvider = "model_provider"
	metaKeyErrorMessage  = "error.message"
	metaKeyErrorStack    = "error.stack"
	metaKeyErrorType     = "error.type"
	modelUnknown         = "custom"
)

func standardSpanEventTags(cfg *config.Config, mlApp, service, sessionID string, status transport.SpanStatus, errorType, integration string) map[string]string {
	errorValue := "0"
	if status == transport.SpanStatusError {
		errorValue = "1"
	}
	tags := map[string]string{
		"version":           cfg.TracerConfig.Version,
		"env":               cfg.TracerConfig.Env,
		"service":           service,
		tagKeySource:        tagValueSource,
		"ml_app":            mlApp,
		tagKeyTracerVersion: version.Tag,
		tagKeyLanguage:      tagValueLanguage,
		tagKeyError:         errorValue,
	}
	if sessionID != "" {
		tags["session_id"] = sessionID
	}
	if errorType != "" {
		tags[tagKeyErrorType] = errorType
	}
	if integration != "" {
		tags["integration"] = integration
	}
	return tags
}

func normalizeModel(kind SpanKind, modelName, modelProvider string) (name, provider string, ok bool) {
	if !((kind == SpanKindLLM || kind == SpanKindEmbedding) && modelName != "" || modelProvider != "") {
		return "", "", false
	}
	name = modelName
	if name == "" {
		name = modelUnknown
	}
	provider = strings.ToLower(modelProvider)
	if provider == "" {
		provider = modelUnknown
	}
	return name, provider, true
}

func NewSpanEventMeta(kind SpanKind) map[string]any {
	return map[string]any{"span.kind": string(kind)}
}

func EnsureSpanEventMeta(event *transport.LLMObsSpanEvent) map[string]any {
	if event.Meta == nil {
		event.Meta = make(map[string]any)
	}
	return event.Meta
}

func SpanEventKind(event *transport.LLMObsSpanEvent) SpanKind {
	kind, _ := event.Meta["span.kind"].(string)
	return SpanKind(kind)
}

func ApplySpanEventDefaults(event *transport.LLMObsSpanEvent) {
	EnsureSpanEventMeta(event)
	if event.ParentID == "" {
		event.ParentID = DefaultParentID
	}
	if event.Name == "" {
		event.Name = string(SpanEventKind(event))
	}
	if event.Status == "" {
		event.Status = transport.SpanStatusOK
	}
	if event.DDAttributes.SpanID == "" {
		event.DDAttributes.SpanID = event.SpanID
	}
	if event.DDAttributes.TraceID == "" {
		event.DDAttributes.TraceID = event.TraceID
	}
}

func SetSpanModelMeta(meta map[string]any, kind SpanKind, modelName, modelProvider string) {
	name, provider, ok := normalizeModel(kind, modelName, modelProvider)
	if !ok {
		delete(meta, metaKeyModelName)
		delete(meta, metaKeyModelProvider)
		return
	}
	meta[metaKeyModelName] = name
	meta[metaKeyModelProvider] = provider
}

func SetSpanErrorMeta(meta map[string]any, msg *transport.ErrorMessage) {
	if msg == nil {
		return
	}
	meta[metaKeyErrorMessage] = msg.Message
	meta[metaKeyErrorStack] = msg.Stack
	meta[metaKeyErrorType] = msg.Type
}

func (l *LLMObs) submitLLMObsSpan(span *Span) {
	l.spanEventsCh <- l.llmobsSpanEvent(span)
}

func (l *LLMObs) llmobsSpanEvent(span *Span) *transport.LLMObsSpanEvent {
	spanKind := span.spanKind
	meta := NewSpanEventMeta(spanKind)
	SetSpanModelMeta(meta, spanKind, span.llmCtx.modelName, span.llmCtx.modelProvider)

	metadata := span.llmCtx.metadata
	if len(metadata) > 0 {
		metadata = maps.Clone(metadata)
	} else {
		metadata = make(map[string]any)
	}
	if spanKind == SpanKindAgent && span.llmCtx.agentManifest != "" {
		metadata["agent_manifest"] = span.llmCtx.agentManifest
	}

	input := make(map[string]any)
	output := make(map[string]any)

	if spanKind == SpanKindLLM && len(span.llmCtx.inputMessages) > 0 {
		input["messages"] = span.llmCtx.inputMessages
	} else if txt := span.llmCtx.inputText; len(txt) > 0 {
		input["value"] = txt
	}

	if spanKind == SpanKindLLM && len(span.llmCtx.outputMessages) > 0 {
		output["messages"] = span.llmCtx.outputMessages
	} else if txt := span.llmCtx.outputText; len(txt) > 0 {
		output["value"] = txt
	}

	if spanKind == SpanKindExperiment {
		if expectedOut := span.llmCtx.experimentExpectedOutput; expectedOut != nil {
			meta["expected_output"] = expectedOut
		}
		if expInput := span.llmCtx.experimentInput; expInput != nil {
			meta["input"] = expInput
		}
		if out := span.llmCtx.experimentOutput; out != nil {
			meta["output"] = out
		}
	}

	if spanKind == SpanKindEmbedding {
		if inputDocs := span.llmCtx.inputDocuments; len(inputDocs) > 0 {
			input["documents"] = inputDocs
		}
	}
	if spanKind == SpanKindRetrieval {
		if outputDocs := span.llmCtx.outputDocuments; len(outputDocs) > 0 {
			output["documents"] = outputDocs
		}
	}
	if inputPrompt := span.llmCtx.prompt; inputPrompt != nil {
		if spanKind != SpanKindLLM {
			log.Warn("llmobs: dropping prompt on non-LLM span kind, annotating prompts is only supported for LLM span kinds")
		} else {
			input["prompt"] = promptPayload{Prompt: *inputPrompt, MLApp: span.mlApp}
		}
	}

	if toolDefinitions := span.llmCtx.toolDefinitions; len(toolDefinitions) > 0 {
		meta["tool_definitions"] = toolDefinitions
	}
	if intent := span.llmCtx.intent; intent != "" {
		if spanKind != SpanKindTool {
			log.Warn("llmobs: dropping intent on non-tool span kind, annotating intent is only supported for tool span kinds")
		} else {
			meta["intent"] = intent
		}
	}
	if toolVersion := span.llmCtx.toolVersion; toolVersion != "" {
		meta["tool.version"] = toolVersion
	}

	spanStatus := transport.SpanStatusOK
	var errMsg *transport.ErrorMessage
	if span.error != nil {
		spanStatus = transport.SpanStatusError
		errMsg = transport.NewErrorMessage(span.error)
		SetSpanErrorMeta(meta, errMsg)
	}

	if len(input) > 0 {
		meta["input"] = input
	}
	if len(output) > 0 {
		meta["output"] = output
	}
	if span.parentAgentSpanID != "" {
		var agentName any = span.parentAgentName
		if span.parentAgentName == "" {
			// id-only: emit explicit JSON null, matching the Python/Node wire shape.
			agentName = nil
		}
		meta["agent_attribution"] = map[string]any{
			"pagent_name":    agentName,
			"pagent_span_id": span.parentAgentSpanID,
		}
	}

	spanID := span.apm.SpanID()
	parentID := DefaultParentID
	if span.parent != nil {
		parentID = span.parent.apm.SpanID()
	} else if span.propagated != nil {
		parentID = span.propagated.SpanID
	}
	if span.llmTraceID == "" {
		log.Warn("llmobs: span has no trace ID")
		span.llmTraceID = newLLMObsTraceID()
	}

	tags := make(map[string]string)
	for k, v := range l.Config.TracerConfig.DDTags {
		tags[k] = fmt.Sprintf("%v", v)
	}
	sessionID := span.propagatedSessionID()
	errorType := ""
	if errMsg != nil {
		errorType = errMsg.Type
	}
	maps.Copy(tags, standardSpanEventTags(
		l.Config,
		span.mlApp,
		l.Config.TracerConfig.Service,
		sessionID,
		spanStatus,
		errorType,
		span.integration,
	))
	maps.Copy(tags, span.llmCtx.tags)

	setMetadataCostTags(metadata, validateCostTags(span, tags))
	if len(metadata) > 0 {
		meta["metadata"] = metadata
	}

	tagsSlice := make([]string, 0, len(tags))
	for k, v := range tags {
		tagsSlice = append(tagsSlice, fmt.Sprintf("%s:%s", k, v))
	}

	ddAttrs := transport.DDAttributes{
		SpanID:     spanID,
		TraceID:    span.llmTraceID,
		APMTraceID: span.apm.TraceID(),
	}
	if span.scope != "" {
		ddAttrs.Scope = span.scope
	}

	ev := &transport.LLMObsSpanEvent{
		SpanID:           spanID,
		TraceID:          span.llmTraceID,
		ParentID:         parentID,
		SessionID:        sessionID,
		Tags:             tagsSlice,
		Name:             span.name,
		StartNS:          span.startTime.UnixNano(),
		Duration:         span.finishTime.Sub(span.startTime),
		Status:           spanStatus,
		StatusMessage:    "",
		Meta:             meta,
		Metrics:          span.llmCtx.metrics,
		CollectionErrors: nil,
		SpanLinks:        toTransportSpanLinks(span.spanLinks),
		DDAttributes:     ddAttrs,
	}
	if b, err := json.Marshal(ev); err == nil {
		rawSize := len(b)
		trackSpanEventRawSize(ev, rawSize)

		truncated := false
		if rawSize > SizeLimitEVPEvent {
			log.Warn(
				"llmobs: dropping llmobs span event input/output because its size (%s) exceeds the event size limit (5MB)",
				readableBytes(rawSize),
			)
			truncated = DropSpanEventIO(ev)
			if !truncated {
				log.Debug("llmobs: attempted to drop span event IO but it was not present")
			}
		}
		actualSize := rawSize
		if truncated {
			if b, err := json.Marshal(ev); err == nil {
				actualSize = len(b)
			}
		}
		trackSpanEventSize(ev, actualSize, truncated)
	}
	return ev
}

func toTransportSpanLinks(links []SpanLink) []transport.SpanLink {
	if len(links) == 0 {
		return nil
	}
	out := make([]transport.SpanLink, len(links))
	for i, link := range links {
		out[i] = transport.SpanLink{
			TraceID:    strconv.FormatUint(link.TraceID, 10),
			SpanID:     strconv.FormatUint(link.SpanID, 10),
			Attributes: link.Attributes,
			Tracestate: link.Tracestate,
			Flags:      link.Flags,
		}
		if link.TraceIDHigh != 0 {
			out[i].TraceIDHigh = strconv.FormatUint(link.TraceIDHigh, 10)
		}
	}
	return out
}

// validateCostTags runs after all event tags are assembled so SDK-injected tag keys can be referenced.
func validateCostTags(span *Span, finalTags map[string]string) []string {
	costTags := span.llmCtx.costTags
	if len(costTags) == 0 {
		return nil
	}

	validated := make([]string, 0, len(costTags))
	missing := 0
	for _, costTag := range costTags {
		if _, ok := finalTags[costTag]; !ok {
			log.Warn("llmobs: cost_tags entry %q must reference a key present in span tags. Skipping entry.", costTag)
			missing++
			continue
		}
		validated = append(validated, costTag)
	}

	if missing > 0 {
		trackCostTagsSubmitted(span, missing, "annotate", "error", "missing_span_tag")
	}
	if len(validated) > 0 {
		trackCostTagsSubmitted(span, len(validated), "annotate", "success", "none")
	}
	return validated
}

func setMetadataCostTags(metadata map[string]any, costTags []string) {
	if len(costTags) == 0 {
		return
	}

	ddMetadata, ok := metadata["_dd"].(map[string]any)
	if ok {
		ddMetadata = maps.Clone(ddMetadata)
	} else {
		ddMetadata = make(map[string]any)
	}
	ddMetadata["cost_tags"] = append([]string(nil), costTags...)
	metadata["_dd"] = ddMetadata
}

// DropSpanEventIO drops input and output values from ev.
func DropSpanEventIO(ev *transport.LLMObsSpanEvent) bool {
	if ev == nil {
		return false
	}
	droppedIO := false
	if _, ok := ev.Meta["input"]; ok {
		ev.Meta["input"] = map[string]any{"value": droppedValueText}
		droppedIO = true
	}
	if _, ok := ev.Meta["output"]; ok {
		ev.Meta["output"] = map[string]any{"value": droppedValueText}
		droppedIO = true
	}
	if droppedIO {
		ev.CollectionErrors = []string{collectionErrorDroppedIO}
	} else {
		log.Debug("llmobs: attempted to drop span event IO but it was not present")
	}
	return droppedIO
}
