// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

// ExportValidationCode identifies why an offline event cannot be exported.
type ExportValidationCode string

const (
	ExportCodeMissingID     ExportValidationCode = "missing_id"
	ExportCodeMissingKind   ExportValidationCode = "missing_kind"
	ExportCodeInvalidKind   ExportValidationCode = "invalid_kind"
	ExportCodeInvalidStatus ExportValidationCode = "invalid_status"
	ExportCodeInvalidLink   ExportValidationCode = "invalid_link"
	ExportCodeMissingLabel  ExportValidationCode = "missing_label"
	ExportCodeInvalidJoin   ExportValidationCode = "invalid_join"
	ExportCodeInvalidValue  ExportValidationCode = "invalid_value"
	ExportCodeTypeMismatch  ExportValidationCode = "type_mismatch"
	ExportCodeNotEncodable  ExportValidationCode = "not_encodable"
	ExportCodeTooLarge      ExportValidationCode = "too_large"
)

// ExportValidationError describes an offline input row that was not sent.
type ExportValidationError struct {
	Index  int
	Code   ExportValidationCode
	Reason string
	cause  error
}

func (e ExportValidationError) Error() string {
	return fmt.Sprintf("llmobs/export: row %d rejected (%s): %s", e.Index, e.Code, e.Reason)
}

func (e ExportValidationError) Unwrap() error {
	return e.cause
}

// ValidateExportSpan checks the fields required by the LLM Obs intake.
func ValidateExportSpan(event transport.LLMObsSpanEvent) *ExportValidationError {
	if event.SpanID == "" || event.TraceID == "" {
		return &ExportValidationError{Code: ExportCodeMissingID, Reason: "missing span_id or trace_id"}
	}
	kind := SpanEventKind(&event)
	if kind == "" {
		return &ExportValidationError{Code: ExportCodeMissingKind, Reason: `missing meta["span.kind"]`}
	}
	if !isExportSpanKind(kind) {
		return &ExportValidationError{Code: ExportCodeInvalidKind, Reason: fmt.Sprintf("invalid span kind %q", kind)}
	}
	if event.Status != "" && !isValidSpanStatus(event.Status) {
		return &ExportValidationError{Code: ExportCodeInvalidStatus, Reason: fmt.Sprintf("invalid status %q", event.Status)}
	}
	for i, link := range event.SpanLinks {
		switch {
		case !canonicalDecimalID(link.TraceID):
			return &ExportValidationError{
				Code:   ExportCodeInvalidLink,
				Reason: fmt.Sprintf("span_links[%d].trace_id must be a canonical decimal uint64", i),
			}
		case !canonicalDecimalID(link.SpanID):
			return &ExportValidationError{
				Code:   ExportCodeInvalidLink,
				Reason: fmt.Sprintf("span_links[%d].span_id must be a canonical decimal uint64", i),
			}
		}
	}
	return nil
}

func isExportSpanKind(kind SpanKind) bool {
	switch kind {
	case SpanKindLLM, SpanKindAgent, SpanKindWorkflow, SpanKindTask,
		SpanKindTool, SpanKindEmbedding, SpanKindRetrieval, transport.SpanKindStep:
		return true
	default:
		return false
	}
}

func isValidSpanStatus(status transport.SpanStatus) bool {
	return status == transport.SpanStatusOK || status == transport.SpanStatusError
}

// BuildExportSpan clones a validated transport span and applies client defaults.
func BuildExportSpan(event transport.LLMObsSpanEvent, cfg *config.Config, service string) *transport.LLMObsSpanEvent {
	span := event
	span.Tags = slices.Clone(event.Tags)
	span.Meta = maps.Clone(event.Meta)
	span.Metrics = maps.Clone(event.Metrics)
	span.CollectionErrors = slices.Clone(event.CollectionErrors)
	span.SpanLinks = slices.Clone(event.SpanLinks)

	ApplySpanEventDefaults(&span)
	if span.Service == "" {
		span.Service = service
	}
	errorType := ""
	if span.Status == transport.SpanStatusError {
		errorType, _ = span.Meta[metaKeyErrorType].(string)
	}
	for key, value := range standardSpanEventTags(cfg, cfg.MLApp, span.Service, span.SessionID, span.Status, errorType, "") {
		switch key {
		case "service", "session_id":
			span.Tags = replaceExportTag(span.Tags, key, value)
		default:
			span.Tags = stampExportTag(span.Tags, key, value)
		}
	}
	return &span
}

// BuildExportEvaluation validates and lowers the existing evaluation config.
func BuildExportEvaluation(metric EvaluationConfig, defaultMLApp string) (*transport.LLMObsMetric, *ExportValidationError) {
	joinOn, valueType, validation := validateEvaluation(metric)
	if validation != nil {
		return nil, exportEvaluationValidationError(validation)
	}
	if metric.ScoreValue != nil && (math.IsNaN(*metric.ScoreValue) || math.IsInf(*metric.ScoreValue, 0)) {
		return nil, &ExportValidationError{
			Code: ExportCodeInvalidValue, Reason: "score value must be a finite number",
		}
	}
	metricType, validation := resolveEvaluationMetricType(metric.MetricType, valueType)
	if validation != nil {
		return nil, exportEvaluationValidationError(validation)
	}
	return lowerEvaluation(metric, defaultMLApp, joinOn, metricType), nil
}

func exportEvaluationValidationError(validation *evaluationValidationError) *ExportValidationError {
	var code ExportValidationCode
	switch validation.kind {
	case evaluationMissingLabel:
		code = ExportCodeMissingLabel
	case evaluationInvalidJoin:
		code = ExportCodeInvalidJoin
	case evaluationInvalidValue:
		code = ExportCodeInvalidValue
	case evaluationTypeMismatch:
		code = ExportCodeTypeMismatch
	}
	return &ExportValidationError{Code: code, Reason: validation.reason, cause: validation.cause}
}

func canonicalDecimalID(id string) bool {
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == id
}

func stampExportTag(tags []string, key, value string) []string {
	prefix := key + ":"
	for _, tag := range tags {
		if strings.HasPrefix(tag, prefix) && tag != prefix {
			return tags
		}
	}
	if value == "" {
		return tags
	}
	out := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if tag != prefix {
			out = append(out, tag)
		}
	}
	return append(out, prefix+value)
}

func replaceExportTag(tags []string, key, value string) []string {
	if value == "" {
		return tags
	}
	prefix := key + ":"
	out := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if !strings.HasPrefix(tag, prefix) {
			out = append(out, tag)
		}
	}
	return append(out, prefix+value)
}
