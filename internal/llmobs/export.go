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
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// ExportValidationCode identifies why an offline event cannot be exported.
type ExportValidationCode string

const (
	ExportCodeMissingID     ExportValidationCode = "missing_id"
	ExportCodeMissingKind   ExportValidationCode = "missing_kind"
	ExportCodeInvalidKind   ExportValidationCode = "invalid_kind"
	ExportCodeInvalidStatus ExportValidationCode = "invalid_status"
	ExportCodeMissingLabel  ExportValidationCode = "missing_label"
	ExportCodeInvalidJoin   ExportValidationCode = "invalid_join"
	ExportCodeInvalidValue  ExportValidationCode = "invalid_value"
	ExportCodeTypeMismatch  ExportValidationCode = "type_mismatch"
	ExportCodeNotEncodable  ExportValidationCode = "not_encodable"
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

var (
	validExportSpanKinds = map[SpanKind]struct{}{
		SpanKindLLM: {}, SpanKindAgent: {}, SpanKindWorkflow: {}, SpanKindTask: {},
		SpanKindTool: {}, SpanKindEmbedding: {}, SpanKindRetrieval: {},
	}
	validExportSpanStatuses = map[SpanStatus]struct{}{
		SpanStatusOK: {}, SpanStatusError: {},
	}
)

// ValidateExportSpan checks the fields required by the LLM Obs intake.
func ValidateExportSpan(event transport.LLMObsSpanEvent) *ExportValidationError {
	if event.SpanID == "" || event.TraceID == "" {
		return &ExportValidationError{Code: ExportCodeMissingID, Reason: "missing span_id or trace_id"}
	}
	kind := exportSpanKind(event)
	if kind == "" {
		return &ExportValidationError{Code: ExportCodeMissingKind, Reason: `missing meta["span.kind"]`}
	}
	if _, ok := validExportSpanKinds[kind]; !ok {
		return &ExportValidationError{Code: ExportCodeInvalidKind, Reason: fmt.Sprintf("invalid span kind %q", kind)}
	}
	status := SpanStatus(event.Status)
	if _, ok := validExportSpanStatuses[status]; event.Status != "" && !ok {
		return &ExportValidationError{Code: ExportCodeInvalidStatus, Reason: fmt.Sprintf("invalid status %q", event.Status)}
	}
	return nil
}

// BuildExportSpan clones a validated transport span and applies client defaults.
func BuildExportSpan(event transport.LLMObsSpanEvent, service, env, spanVersion, mlApp string) *transport.LLMObsSpanEvent {
	span := event
	span.Tags = slices.Clone(event.Tags)
	span.Meta = maps.Clone(event.Meta)
	span.Metrics = maps.Clone(event.Metrics)
	span.CollectionErrors = slices.Clone(event.CollectionErrors)
	span.SpanLinks = slices.Clone(event.SpanLinks)

	if span.Meta == nil {
		span.Meta = make(map[string]any)
	}
	kind := exportSpanKind(span)
	span.Meta["span"] = map[string]any{"kind": string(kind)}
	span.Meta["span.kind"] = string(kind)
	if modelName, modelProvider, ok := NormalizeModel(kind, span.ModelName, span.ModelProvider); ok {
		span.Meta[MetaKeyModelName] = modelName
		span.Meta[MetaKeyModelProvider] = modelProvider
	}
	if span.Input != "" {
		span.Meta["input"] = map[string]any{"value": span.Input}
	}
	if span.Output != "" {
		span.Meta["output"] = map[string]any{"value": span.Output}
	}
	if len(span.Metadata) > 0 {
		span.Meta["metadata"] = span.Metadata
	}
	if !span.Start.IsZero() {
		span.StartNS = span.Start.UnixNano()
	}
	if span.ParentID == "" {
		span.ParentID = defaultParentID
	}
	if span.Name == "" {
		span.Name = string(kind)
	}
	if span.Status == "" {
		span.Status = string(SpanStatusOK)
	}
	if span.Service == "" {
		span.Service = service
	}
	if span.DDAttributes.SpanID == "" {
		span.DDAttributes.SpanID = span.SpanID
	}
	if span.DDAttributes.TraceID == "" {
		span.DDAttributes.TraceID = span.TraceID
	}
	if span.DDAttributes.APMTraceID == "" {
		span.DDAttributes.APMTraceID = span.APMTraceID
	}

	var errMsg *transport.ErrorMessage
	if SpanStatus(span.Status) == SpanStatusError {
		message := span.ErrorMessage
		if message == "" {
			message = span.StatusMessage
		}
		if message == "" {
			message, _ = span.Meta["error.message"].(string)
		}
		errorType := span.ErrorType
		if errorType == "" {
			errorType, _ = span.Meta["error.type"].(string)
		}
		errorStack := span.ErrorStack
		if errorStack == "" {
			errorStack, _ = span.Meta["error.stack"].(string)
		}
		errMsg = &transport.ErrorMessage{
			Message: message,
			Type:    errorType,
			Stack:   errorStack,
		}
	}
	SetErrorMeta(span.Meta, errMsg)

	span.Tags = stampExportTags(span.Tags, env, spanVersion, mlApp)
	span.Tags = replaceExportTag(span.Tags, "service", span.Service)
	span.Tags = replaceExportTag(span.Tags, "session_id", span.SessionID)
	span.Tags = stampExportTag(span.Tags, TagKeyError, exportErrorTag(SpanStatus(span.Status)))
	if errMsg != nil {
		span.Tags = stampExportTag(span.Tags, TagKeyErrorType, errMsg.Type)
	}
	return &span
}

// BuildExportEvaluation validates and lowers the existing evaluation config.
func BuildExportEvaluation(metric EvaluationConfig, defaultMLApp string) (*transport.LLMObsMetric, *ExportValidationError) {
	return buildEvaluation(metric, defaultMLApp, true)
}

func buildEvaluation(metric EvaluationConfig, defaultMLApp string, rejectNonFinite bool) (*transport.LLMObsMetric, *ExportValidationError) {
	if metric.Label == "" {
		return nil, &ExportValidationError{
			Code: ExportCodeMissingLabel, Reason: "missing label", cause: errInvalidMetricLabel,
		}
	}

	joinOn, err := BuildEvaluationJoin(metric.SpanID, metric.TraceID, metric.TagKey, metric.TagValue)
	if err != nil {
		return nil, &ExportValidationError{
			Code: ExportCodeInvalidJoin, Reason: err.Error(), cause: err,
		}
	}

	values := 0
	if metric.CategoricalValue != nil {
		values++
	}
	if metric.ScoreValue != nil {
		values++
	}
	if metric.BooleanValue != nil {
		values++
	}
	if metric.JSONValue != nil {
		values++
	}
	if values != 1 {
		return nil, &ExportValidationError{
			Code: ExportCodeInvalidValue, Reason: "exactly one of categorical, score, boolean, or json value must be set",
		}
	}
	if metric.JSONValue != nil && len(metric.JSONValue) == 0 {
		return nil, &ExportValidationError{
			Code: ExportCodeInvalidValue, Reason: "json_value must not be empty",
		}
	}
	if rejectNonFinite && metric.ScoreValue != nil && (math.IsNaN(*metric.ScoreValue) || math.IsInf(*metric.ScoreValue, 0)) {
		return nil, &ExportValidationError{
			Code: ExportCodeInvalidValue, Reason: "score value must be a finite number",
		}
	}

	valueType := evaluationValueType(metric)
	metricType := metric.MetricType
	switch {
	case metricType == "":
		metricType = valueType
	case metricType != EvalMetricTypeCategorical &&
		metricType != EvalMetricTypeScore &&
		metricType != EvalMetricTypeBoolean &&
		metricType != EvalMetricTypeJSON:
		return nil, &ExportValidationError{
			Code:   ExportCodeTypeMismatch,
			Reason: fmt.Sprintf("invalid metric type %q (want categorical, score, boolean, or json)", metricType),
		}
	case metricType != valueType:
		return nil, &ExportValidationError{
			Code:   ExportCodeTypeMismatch,
			Reason: fmt.Sprintf("metric type %q does not match the %s value provided", metricType, valueType),
		}
	}

	mlApp := metric.MLApp
	if mlApp == "" {
		mlApp = defaultMLApp
	}

	return &transport.LLMObsMetric{
		JoinOn:           joinOn,
		Label:            metric.Label,
		MetricType:       string(metricType),
		TimestampMS:      evaluationTimestamp(metric),
		MLApp:            mlApp,
		Tags:             exportEvaluationTags(metric.Tags),
		Assessment:       metric.Assessment,
		Reasoning:        metric.Reasoning,
		Metadata:         metric.Metadata,
		CategoricalValue: metric.CategoricalValue,
		ScoreValue:       metric.ScoreValue,
		BooleanValue:     metric.BooleanValue,
		JSONValue:        metric.JSONValue,
	}, nil
}

func exportSpanKind(event transport.LLMObsSpanEvent) SpanKind {
	if event.Kind != "" {
		return SpanKind(event.Kind)
	}
	kind, _ := event.Meta["span.kind"].(string)
	return SpanKind(kind)
}

func exportErrorTag(status SpanStatus) string {
	if status == SpanStatusError {
		return "1"
	}
	return "0"
}

func stampExportTags(tags []string, env, spanVersion, mlApp string) []string {
	tags = stampExportTag(tags, "env", env)
	tags = stampExportTag(tags, "version", spanVersion)
	tags = stampExportTag(tags, "ml_app", mlApp)
	tags = stampExportTag(tags, TagKeySource, TagValueSource)
	tags = stampExportTag(tags, TagKeyLanguage, TagValueLanguage)
	return stampExportTag(tags, TagKeyTracerVersion, version.Tag)
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

func evaluationValueType(metric EvaluationConfig) EvalMetricType {
	switch {
	case metric.CategoricalValue != nil:
		return EvalMetricTypeCategorical
	case metric.ScoreValue != nil:
		return EvalMetricTypeScore
	case metric.BooleanValue != nil:
		return EvalMetricTypeBoolean
	default:
		return EvalMetricTypeJSON
	}
}

func evaluationTimestamp(metric EvaluationConfig) int64 {
	if metric.TimestampMS != 0 {
		return metric.TimestampMS
	}
	if !metric.Timestamp.IsZero() {
		return metric.Timestamp.UnixMilli()
	}
	return 0
}

func exportEvaluationTags(tags []string) []string {
	prefix := TagKeyTracerVersion + ":"
	out := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if !strings.HasPrefix(tag, prefix) {
			out = append(out, tag)
		}
	}
	return append(out, prefix+version.Tag)
}
