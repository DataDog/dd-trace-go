// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"fmt"
	"maps"
	"math"
	"strings"
	"time"

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

// ExportSpanEvent is a caller-built LLM Obs span.
type ExportSpanEvent struct {
	TraceID    string
	SpanID     string
	ParentID   string
	APMTraceID string

	SessionID string
	Name      string
	Service   string

	Start    time.Time
	Duration time.Duration

	Status        SpanStatus
	StatusMessage string
	ErrorMessage  string
	ErrorType     string
	ErrorStack    string

	Kind          SpanKind
	ModelName     string
	ModelProvider string
	Input         string
	Output        string
	Metadata      map[string]any

	Metrics   *ExportSpanMetrics
	Tags      []string
	SpanLinks []ExportSpanLink
}

// ExportSpanMetrics contains optional token, timing, and cost metrics.
type ExportSpanMetrics struct {
	InputTokens            *int64
	OutputTokens           *int64
	TotalTokens            *int64
	CacheWriteInputTokens  *int64
	CacheReadInputTokens   *int64
	NonCachedInputTokens   *int64
	ReasoningOutputTokens  *int64
	Ephemeral1HInputTokens *int64
	Ephemeral5MInputTokens *int64
	BillableCharacterCount *int64

	TimeToFirstToken *float64

	EstimatedTotalCost           *float64
	EstimatedInputCost           *float64
	EstimatedOutputCost          *float64
	EstimatedCacheReadInputCost  *float64
	EstimatedCacheWriteInputCost *float64

	Extra map[string]float64
}

// ExportSpanLink links an offline span to another span using caller-owned IDs.
type ExportSpanLink struct {
	SpanID     string
	TraceID    string
	Attributes map[string]string
}

// ExportSpanDefaults supplies values inherited from an export client.
type ExportSpanDefaults struct {
	Service string
	Env     string
	Version string
	MLApp   string
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
func ValidateExportSpan(event ExportSpanEvent) *ExportValidationError {
	switch {
	case event.SpanID == "" || event.TraceID == "":
		return &ExportValidationError{Code: ExportCodeMissingID, Reason: "missing span_id or trace_id"}
	case event.Kind == "":
		return &ExportValidationError{Code: ExportCodeMissingKind, Reason: "missing kind"}
	}
	if _, ok := validExportSpanKinds[event.Kind]; !ok {
		return &ExportValidationError{Code: ExportCodeInvalidKind, Reason: fmt.Sprintf("invalid Kind %q", event.Kind)}
	}
	if _, ok := validExportSpanStatuses[event.Status]; event.Status != "" && !ok {
		return &ExportValidationError{Code: ExportCodeInvalidStatus, Reason: fmt.Sprintf("invalid Status %q", event.Status)}
	}
	return nil
}

// BuildExportSpan lowers a validated manual span to the shared transport model.
func BuildExportSpan(event ExportSpanEvent, defaults ExportSpanDefaults) *transport.LLMObsSpanEvent {
	parentID := event.ParentID
	if parentID == "" {
		parentID = defaultParentID
	}
	name := event.Name
	if name == "" {
		name = string(event.Kind)
	}
	status := event.Status
	if status == "" {
		status = SpanStatusOK
	}
	service := event.Service
	if service == "" {
		service = defaults.Service
	}

	meta := map[string]any{
		"span": map[string]any{"kind": string(event.Kind)},
	}
	if event.Kind != "" {
		meta["span.kind"] = string(event.Kind)
	}
	if modelName, modelProvider, ok := NormalizeModel(event.Kind, event.ModelName, event.ModelProvider); ok {
		meta[MetaKeyModelName] = modelName
		meta[MetaKeyModelProvider] = modelProvider
	}
	if event.Input != "" {
		meta["input"] = map[string]any{"value": event.Input}
	}
	if event.Output != "" {
		meta["output"] = map[string]any{"value": event.Output}
	}
	if len(event.Metadata) > 0 {
		meta["metadata"] = event.Metadata
	}
	errMsg := exportErrorMessage(event, status)
	SetErrorMeta(meta, errMsg)

	span := &transport.LLMObsSpanEvent{
		TraceID:       event.TraceID,
		SpanID:        event.SpanID,
		ParentID:      parentID,
		SessionID:     event.SessionID,
		Name:          name,
		Service:       service,
		StartNS:       exportStartNanos(event.Start),
		Duration:      int64(event.Duration),
		Status:        string(status),
		StatusMessage: event.StatusMessage,
		Meta:          meta,
		Metrics:       exportMetrics(event.Metrics),
		DDAttributes: transport.DDAttributes{
			SpanID:     event.SpanID,
			TraceID:    event.TraceID,
			APMTraceID: event.APMTraceID,
		},
	}
	span.Tags = append([]string{}, event.Tags...)
	span.Tags = stampExportTags(span.Tags, defaults)
	span.Tags = replaceExportTag(span.Tags, "service", span.Service)
	span.Tags = replaceExportTag(span.Tags, "session_id", event.SessionID)
	span.Tags = stampExportTag(span.Tags, TagKeyError, exportErrorTag(status))
	if errMsg != nil {
		span.Tags = stampExportTag(span.Tags, TagKeyErrorType, errMsg.Type)
	}
	for _, link := range event.SpanLinks {
		span.SpanLinks = append(span.SpanLinks, transport.SpanLink{
			SpanID:     link.SpanID,
			TraceID:    link.TraceID,
			Attributes: link.Attributes,
		})
	}
	return span
}

// ExportEvaluationMetric is a caller-built LLM Obs evaluation metric.
type ExportEvaluationMetric struct {
	SpanID   string
	TraceID  string
	TagKey   string
	TagValue string

	Label      string
	MetricType EvalMetricType

	CategoricalValue *string
	ScoreValue       *float64
	BooleanValue     *bool
	JSONValue        map[string]any

	Timestamp time.Time
	MLApp     string
	Tags      []string

	Assessment string
	Reasoning  string
	Metadata   map[string]any
}

// BuildExportEvaluation validates and lowers a manual evaluation metric.
func BuildExportEvaluation(metric ExportEvaluationMetric, defaultMLApp string) (*transport.LLMObsMetric, *ExportValidationError) {
	return buildExportEvaluation(metric, defaultMLApp, true)
}

func buildExportEvaluation(metric ExportEvaluationMetric, defaultMLApp string, rejectNonFinite bool) (*transport.LLMObsMetric, *ExportValidationError) {
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

	valueType := exportMetricValueType(metric)
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
			Reason: fmt.Sprintf("invalid MetricType %q (want categorical, score, boolean, or json)", metricType),
		}
	case metricType != valueType:
		return nil, &ExportValidationError{
			Code:   ExportCodeTypeMismatch,
			Reason: fmt.Sprintf("MetricType %q does not match the %s value provided", metricType, valueType),
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
		TimestampMS:      exportTimestampMillis(metric.Timestamp),
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

func exportMetrics(metrics *ExportSpanMetrics) map[string]float64 {
	if metrics == nil {
		return nil
	}
	out := make(map[string]float64, len(metrics.Extra)+16)
	maps.Copy(out, metrics.Extra)
	putExportInt(out, "input_tokens", metrics.InputTokens)
	putExportInt(out, "output_tokens", metrics.OutputTokens)
	putExportInt(out, "total_tokens", metrics.TotalTokens)
	putExportInt(out, "cache_write_input_tokens", metrics.CacheWriteInputTokens)
	putExportInt(out, "cache_read_input_tokens", metrics.CacheReadInputTokens)
	putExportInt(out, "non_cached_input_tokens", metrics.NonCachedInputTokens)
	putExportInt(out, "reasoning_output_tokens", metrics.ReasoningOutputTokens)
	putExportInt(out, "ephemeral_1h_input_tokens", metrics.Ephemeral1HInputTokens)
	putExportInt(out, "ephemeral_5m_input_tokens", metrics.Ephemeral5MInputTokens)
	putExportInt(out, "billable_character_count", metrics.BillableCharacterCount)
	putExportFloat(out, "time_to_first_token", metrics.TimeToFirstToken)
	putExportFloat(out, "estimated_total_cost", metrics.EstimatedTotalCost)
	putExportFloat(out, "estimated_input_cost", metrics.EstimatedInputCost)
	putExportFloat(out, "estimated_output_cost", metrics.EstimatedOutputCost)
	putExportFloat(out, "estimated_cache_read_input_cost", metrics.EstimatedCacheReadInputCost)
	putExportFloat(out, "estimated_cache_write_input_cost", metrics.EstimatedCacheWriteInputCost)
	if len(out) == 0 {
		return nil
	}
	return out
}

func putExportInt(metrics map[string]float64, key string, value *int64) {
	if value != nil {
		metrics[key] = float64(*value)
	}
}

func putExportFloat(metrics map[string]float64, key string, value *float64) {
	if value != nil {
		metrics[key] = *value
	}
}

func exportErrorMessage(event ExportSpanEvent, status SpanStatus) *transport.ErrorMessage {
	if status != SpanStatusError {
		return nil
	}
	message := event.ErrorMessage
	if message == "" {
		message = event.StatusMessage
	}
	return &transport.ErrorMessage{
		Message: message,
		Type:    event.ErrorType,
		Stack:   event.ErrorStack,
	}
}

func exportErrorTag(status SpanStatus) string {
	if status == SpanStatusError {
		return "1"
	}
	return "0"
}

func exportStartNanos(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	return start.UnixNano()
}

func stampExportTags(tags []string, defaults ExportSpanDefaults) []string {
	tags = stampExportTag(tags, "env", defaults.Env)
	tags = stampExportTag(tags, "version", defaults.Version)
	tags = stampExportTag(tags, "ml_app", defaults.MLApp)
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

func exportMetricValueType(metric ExportEvaluationMetric) EvalMetricType {
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

func exportTimestampMillis(timestamp time.Time) int64 {
	if timestamp.IsZero() {
		return 0
	}
	return timestamp.UnixMilli()
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
