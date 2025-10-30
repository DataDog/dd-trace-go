// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"errors"
	"fmt"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const (
	telemetryMetricInitTime          = "init_time"
	telemetryMetricEnabled           = "product_enabled"
	telemetryMetricRawSpanSize       = "span.raw_size"
	telemetryMetricSpanSize          = "span.size"
	telemetryMetricSpanStarted       = "span.start"
	telemetryMetricSpanFinished      = "span.finished"
	telemetryMetricDroppedSpanEvents = "dropped_span_events"
	telemetryMetricDroppedEvalEvents = "dropped_eval_events"
	telemetryMetricAnnotations       = "annotations"
	telemetryMetricEvalsSubmitted    = "evals_submitted"
	telemetryMetricUserFlushes       = "user_flush"
)

var telemetryErrorTypes = map[error]string{
	errInvalidMetricLabel: "invalid_metric_label",
	errFinishedSpan:       "invalid_finished_span",
	errInvalidSpanJoin:    "invalid_span",
	errInvalidTagJoin:     "invalid_tag_join",
}

func trackLLMObsStart(startTime time.Time, err error, cfg config.Config) {
	tags := []string{
		fmt.Sprintf("agentless:%s", boolTag(cfg.ResolvedAgentlessEnabled)),
		fmt.Sprintf("site:%s", cfg.TracerConfig.Site),
		fmt.Sprintf("ml_app:%s", valOrNA(cfg.MLApp)),
	}
	if err != nil {
		tags = append(tags, errTelemetryTags(err)...)
	}

	initTimeMs := float64(time.Since(startTime).Milliseconds())
	telemetry.Distribution(telemetry.NamespaceMLObs, telemetryMetricInitTime, tags).Submit(initTimeMs)
	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricEnabled, tags).Submit(1)
}

func trackSpanStarted() {
	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricSpanStarted, nil).Submit(1)
}

func trackSpanFinished(span *Span) {
	isRootSpan := span.parent == nil
	hasSessionID := span.sessionID != ""
	integration := span.integration
	autoinstrumented := integration != ""
	spanKind := string(span.spanKind)
	modelProvider := span.llmCtx.modelProvider
	mlApp := span.mlApp
	hasError := span.error != nil

	tags := []string{
		fmt.Sprintf("autoinstrumented:%s", boolTag(autoinstrumented)),
		fmt.Sprintf("has_session_id:%s", boolTag(hasSessionID)),
		fmt.Sprintf("is_root_span:%s", boolTag(isRootSpan)),
		fmt.Sprintf("span_kind:%s", valOrNA(spanKind)),
		fmt.Sprintf("integration:%s", valOrNA(integration)),
		fmt.Sprintf("ml_app:%s", valOrNA(mlApp)),
		fmt.Sprintf("error:%s", boolTag(hasError)),
	}
	if modelProvider != "" {
		tags = append(tags, fmt.Sprintf("model_provider:%s", modelProvider))
	}

	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricSpanFinished, tags).Submit(1)
}

func trackSpanEventRawSize(event *transport.LLMObsSpanEvent, rawSize int) {
	tags := spanEventTags(event)
	telemetry.Distribution(telemetry.NamespaceMLObs, telemetryMetricRawSpanSize, tags).Submit(float64(rawSize))
}

func trackSpanEventSize(event *transport.LLMObsSpanEvent, size int, truncated bool) {
	tags := spanEventTags(event)
	tags = append(tags, fmt.Sprintf("truncated:%s", boolTag(truncated)))
	telemetry.Distribution(telemetry.NamespaceMLObs, telemetryMetricSpanSize, tags).Submit(float64(size))
}

func trackDroppedPayload(numEvents int, metricName string, errType string) {
	tags := []string{"error:1", fmt.Sprintf("error_type:%s", errType)}
	telemetry.Count(telemetry.NamespaceMLObs, metricName, tags).Submit(float64(numEvents))
}

func trackSpanAnnotations(span *Span, err error) {
	tags := errTelemetryTags(err)
	spanKind := ""
	isRootSpan := "0"
	if span != nil {
		spanKind = valOrNA(string(span.spanKind))
		isRootSpan = boolTag(span.parent == nil)
	}
	tags = append(tags,
		fmt.Sprintf("span_kind:%s", spanKind),
		fmt.Sprintf("is_root_span:%s", isRootSpan),
	)
	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricAnnotations, tags).Submit(1)
}

func trackSubmitEvaluationMetric(metric *transport.LLMObsMetric, err error) {
	metricType := "other"
	hasTag := false
	if metric != nil {
		metricType = metric.MetricType
		hasTag = metric.JoinOn.Tag != nil
	}

	tags := errTelemetryTags(err)
	tags = append(tags,
		fmt.Sprintf("metric_type:%s", metricType),
		fmt.Sprintf("custom_joining_key:%s", boolTag(hasTag)),
	)
	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricEvalsSubmitted, tags).Submit(1)
}

func trackUserFlush() {
	tags := errTelemetryTags(nil)
	telemetry.Count(telemetry.NamespaceMLObs, telemetryMetricUserFlushes, tags).Submit(1)
}

func spanEventTags(event *transport.LLMObsSpanEvent) []string {
	spanKind := "N/A"
	if meta, ok := event.Meta["span.kind"]; ok {
		if kind, ok := meta.(string); ok {
			spanKind = kind
		}
	}

	integration := findTagValue(event.Tags, "integration:")
	mlApp := findTagValue(event.Tags, "ml_app:")
	autoInstrumented := integration != ""
	hasError := event.Status == "error"

	return []string{
		fmt.Sprintf("span_kind:%s", spanKind),
		fmt.Sprintf("autoinstrumented:%s", boolTag(autoInstrumented)),
		fmt.Sprintf("error:%s", boolTag(hasError)),
		fmt.Sprintf("integration:%s", valOrNA(integration)),
		fmt.Sprintf("ml_app:%s", valOrNA(mlApp)),
	}
}

func findTagValue(tags []string, prefix string) string {
	for _, tag := range tags {
		if len(tag) > len(prefix) && tag[:len(prefix)] == prefix {
			return tag[len(prefix):]
		}
	}
	return ""
}

func valOrNA(value string) string {
	if value == "" {
		return "n/a"
	}
	return value
}

func errTelemetryTags(err error) []string {
	tags := []string{fmt.Sprintf("error:%s", boolTag(err != nil))}
	if err != nil {
		for targetErr, errType := range telemetryErrorTypes {
			if errors.Is(err, targetErr) {
				tags = append(tags, fmt.Sprintf("error_type:%s", errType))
				break
			}
		}
	}
	return tags
}

func boolTag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
