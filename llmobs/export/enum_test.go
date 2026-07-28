// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/export"
)

// TestEnumWireValues pins the literal each exported enum constant carries. The
// constants are single-sourced from llmobs (which single-sources them from
// internal/llmobs), so godoc renders them as opaque aliases and the values
// documented alongside them could otherwise drift from what reaches the intake.
func TestEnumWireValues(t *testing.T) {
	assert.Equal(t, "llm", string(export.KindLLM))
	assert.Equal(t, "agent", string(export.KindAgent))
	assert.Equal(t, "workflow", string(export.KindWorkflow))
	assert.Equal(t, "task", string(export.KindTask))
	assert.Equal(t, "tool", string(export.KindTool))
	assert.Equal(t, "embedding", string(export.KindEmbedding))
	assert.Equal(t, "retrieval", string(export.KindRetrieval))
	assert.Equal(t, "experiment", string(export.KindExperiment))

	assert.Equal(t, "ok", string(export.StatusOK))
	assert.Equal(t, "error", string(export.StatusError))

	assert.Equal(t, "categorical", string(export.MetricTypeCategorical))
	assert.Equal(t, "score", string(export.MetricTypeScore))
	assert.Equal(t, "boolean", string(export.MetricTypeBoolean))
	assert.Equal(t, "json", string(export.MetricTypeJSON))
}

// TestEnumTypesAreSharedWithLLMObs: the export enums are aliases, not parallel
// copies, so a value from the live package is usable here without conversion. If
// these become distinct defined types the assignments below stop compiling.
func TestEnumTypesAreSharedWithLLMObs(t *testing.T) {
	var (
		k export.Kind       = llmobs.SpanKindWorkflow
		s export.Status     = llmobs.SpanStatusError
		m export.MetricType = llmobs.EvalMetricTypeScore
	)
	assert.Equal(t, export.KindWorkflow, k)
	assert.Equal(t, export.StatusError, s)
	assert.Equal(t, export.MetricTypeScore, m)
}

// TestSubmitSpans_AcceptsEveryLiveKind: validation must accept every kind the live
// tracer can emit on this intake — "experiment" included, which the live
// experiment API produces. Rejecting one would make an offline backfill of that
// population return err==nil with every row dropped, so an outbox caller checking
// only err would mark the batch handled and lose it permanently.
func TestSubmitSpans_AcceptsEveryLiveKind(t *testing.T) {
	kinds := []export.Kind{
		export.KindLLM, export.KindAgent, export.KindWorkflow, export.KindTask,
		export.KindTool, export.KindEmbedding, export.KindRetrieval, export.KindExperiment,
	}
	// Cross-check against the internal enum the live tracer emits from, so a kind
	// added there fails here instead of being silently dropped at export time.
	live := []illmobs.SpanKind{
		illmobs.SpanKindLLM, illmobs.SpanKindAgent, illmobs.SpanKindWorkflow, illmobs.SpanKindTask,
		illmobs.SpanKindTool, illmobs.SpanKindEmbedding, illmobs.SpanKindRetrieval, illmobs.SpanKindExperiment,
	}
	require.Len(t, kinds, len(live), "export's kind set must cover every live SpanKind")

	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	events := make([]export.SpanEvent, 0, len(kinds))
	for i, k := range kinds {
		events = append(events, export.SpanEvent{TraceID: "t", SpanID: strconv.Itoa(i), Kind: k})
	}
	res, err := c.SubmitSpans(context.Background(), events)
	require.NoError(t, err)
	assert.Empty(t, res.ValidationErrors)
	assert.Equal(t, len(kinds), res.Sent)
}

// TestSubmitSpans_RejectsUnknownKindAndStatus: an unrecognized Kind or Status
// POSTs cleanly but lands in a facet nothing queries, so it is a reported
// row-level drop rather than a silent data-quality problem at intake.
func TestSubmitSpans_RejectsUnknownKindAndStatus(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	res, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "t0", SpanID: "s0", Kind: export.KindLLM},                             // valid
		{TraceID: "t1", SpanID: "s1", Kind: "banana"},                                   // unknown kind
		{TraceID: "t2", SpanID: "s2", Kind: export.KindLLM, Status: "kinda-ok"},         // unknown status
		{TraceID: "t3", SpanID: "s3", Kind: export.KindLLM, Status: export.StatusError}, // valid
	})
	require.NoError(t, err)
	require.Len(t, res.ValidationErrors, 2)
	assert.Equal(t, 1, res.ValidationErrors[0].Index)
	assert.Equal(t, export.CodeInvalidKind, res.ValidationErrors[0].Code)
	assert.Equal(t, 2, res.ValidationErrors[1].Index)
	assert.Equal(t, export.CodeInvalidStatus, res.ValidationErrors[1].Code)
	assert.Equal(t, 2, res.Sent)
	assert.Equal(t, 4, res.Sent+res.Failed+res.Dropped)
}

// TestValidationErrorCodes: callers classify a drop from Code, never by matching
// on the free-form Reason text.
func TestValidationErrorCodes(t *testing.T) {
	fake := &fakeTransport{}
	c := newClient(t, fake, "test-app")

	spanRes, err := c.SubmitSpans(context.Background(), []export.SpanEvent{
		{TraceID: "", SpanID: "s", Kind: export.KindLLM},
		{TraceID: "t", SpanID: "s"},
	})
	require.NoError(t, err)
	require.Len(t, spanRes.ValidationErrors, 2)
	assert.Equal(t, export.CodeMissingID, spanRes.ValidationErrors[0].Code)
	assert.Equal(t, export.CodeMissingKind, spanRes.ValidationErrors[1].Code)

	evalRes, err := c.SubmitEvaluations(context.Background(), []export.EvaluationMetric{
		{SpanID: "s", TraceID: "t", ScoreValue: ptr(1.0)},                                           // no label
		{Label: "no-join", ScoreValue: ptr(1.0)},                                                    // no join family
		{SpanID: "s", TraceID: "t", Label: "novalue"},                                               // no value
		{SpanID: "s", TraceID: "t", Label: "mismatch", ScoreValue: ptr(1.0), MetricType: "boolean"}, // type/value mismatch
	})
	require.NoError(t, err)
	require.Len(t, evalRes.ValidationErrors, 4)
	assert.Equal(t, export.CodeMissingLabel, evalRes.ValidationErrors[0].Code)
	assert.Equal(t, export.CodeInvalidJoin, evalRes.ValidationErrors[1].Code)
	assert.Equal(t, export.CodeInvalidValue, evalRes.ValidationErrors[2].Code)
	assert.Equal(t, export.CodeTypeMismatch, evalRes.ValidationErrors[3].Code)

	// ValidationError is an error, so a caller can return one directly.
	var e error = evalRes.ValidationErrors[0]
	assert.Contains(t, e.Error(), "row 0 rejected (missing_label)")
}
