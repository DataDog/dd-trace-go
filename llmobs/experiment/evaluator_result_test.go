// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package experiment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

// captureExperimentEvents returns a mock handler that decodes the POST
// body sent to /api/unstable/llm-obs/v1/experiments/<id>/events into the
// transport struct, appends every recorded metric to events, and
// delegates everything else to the default mock handler.
func captureExperimentEvents(events *[]llmobstransport.ExperimentEvalMetricEvent, mu *sync.Mutex) testtracer.MockResponseFunc {
	base := createMockHandler()
	return func(r *http.Request) *http.Response {
		path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
		if strings.HasPrefix(path, "/api/unstable/llm-obs/v1/experiments/") &&
			strings.HasSuffix(path, "/events") && r.Method == http.MethodPost {
			var body llmobstransport.PushExperimentEventsRequest
			if r.Body != nil {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &body)
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			mu.Lock()
			*events = append(*events, body.Data.Attributes.Metrics...)
			mu.Unlock()
			return handleMockExperimentEvents(r)
		}
		return base(r)
	}
}

// TestEvaluatorResult_BareValueStillWorks asserts an evaluator that
// returns a plain (non-*EvaluatorResult) value continues to work
// exactly as before this feature landed: the value populates the score
// and the new optional fields stay unset on both the public Evaluation
// and the wire payload.
func TestEvaluatorResult_BareValueStillWorks(t *testing.T) {
	var (
		events []llmobstransport.ExperimentEvalMetricEvent
		mu     sync.Mutex
	)
	tt := testTracer(t, testtracer.WithMockResponses(captureExperimentEvents(&events, &mu)))
	defer tt.Stop()

	evs := []experiment.Evaluator{
		experiment.NewEvaluator("bare-score", func(_ context.Context, _ dataset.Record, _ any) (any, error) {
			return 0.42, nil
		}),
	}
	exp, err := experiment.New("evaluator-bare", createTestTask(), createTestDataset(t), evs,
		experiment.WithProjectName("test-project"))
	require.NoError(t, err)

	res, err := exp.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, res.Runs, 1)
	require.NotEmpty(t, res.Runs[0].Results)
	for _, r := range res.Runs[0].Results {
		require.Len(t, r.Evaluations, 1)
		ev := r.Evaluations[0]
		assert.Equal(t, "bare-score", ev.Name)
		assert.Equal(t, 0.42, ev.Value)
		assert.Empty(t, ev.Reasoning)
		assert.Empty(t, ev.Assessment)
		assert.Empty(t, ev.Metadata)
		assert.Empty(t, ev.Tags)
	}

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, events)
	var sawBareMetric bool
	for _, m := range events {
		if m.Label != "bare-score" {
			continue
		}
		sawBareMetric = true
		assert.Empty(t, m.Reasoning)
		assert.Empty(t, m.Assessment)
		assert.Empty(t, m.EvalMetricMetadata)
		// Legacy bare-value evaluators keep the experiment-level run
		// tags on the metric exactly as before this change. The runner
		// stamps run_id and run_iteration as part of runTags.
		var hasRunTag bool
		for _, tag := range m.Tags {
			if strings.HasPrefix(tag, "run_id:") {
				hasRunTag = true
				break
			}
		}
		assert.True(t, hasRunTag, "legacy evaluator should preserve run_id run-tag on the metric; got %v", m.Tags)
	}
	require.True(t, sawBareMetric, "did not find a bare-score metric in the captured events")
}

// TestEvaluatorResult_RichReturnPopulatesAllFields asserts an evaluator
// returning *EvaluatorResult flows Reasoning, Assessment, Metadata, and
// Tags onto both the public Evaluation struct and the wire payload.
func TestEvaluatorResult_RichReturnPopulatesAllFields(t *testing.T) {
	var (
		events []llmobstransport.ExperimentEvalMetricEvent
		mu     sync.Mutex
	)
	tt := testTracer(t, testtracer.WithMockResponses(captureExperimentEvents(&events, &mu)))
	defer tt.Stop()

	evs := []experiment.Evaluator{
		experiment.NewEvaluator("rich-judge", func(_ context.Context, _ dataset.Record, _ any) (any, error) {
			return &experiment.EvaluatorResult{
				Value:      0.80,
				Reasoning:  "the answer covered the central finding",
				Assessment: "pass",
				Metadata:   map[string]any{"anchor": "0.80", "extras": 1},
				Tags:       map[string]string{"axis": "summary_findings"},
			}, nil
		}),
	}
	exp, err := experiment.New("evaluator-rich", createTestTask(), createTestDataset(t), evs,
		experiment.WithProjectName("test-project"))
	require.NoError(t, err)

	res, err := exp.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, res.Runs, 1)

	for _, r := range res.Runs[0].Results {
		require.Len(t, r.Evaluations, 1)
		ev := r.Evaluations[0]
		assert.Equal(t, "rich-judge", ev.Name)
		assert.Equal(t, 0.80, ev.Value)
		assert.Equal(t, "the answer covered the central finding", ev.Reasoning)
		assert.Equal(t, "pass", ev.Assessment)
		assert.Equal(t, map[string]any{"anchor": "0.80", "extras": 1}, ev.Metadata)
		assert.Equal(t, map[string]string{"axis": "summary_findings"}, ev.Tags)
	}

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, events)
	var found bool
	for _, m := range events {
		if m.Label != "rich-judge" {
			continue
		}
		found = true
		assert.Equal(t, "score", m.MetricType)
		require.NotNil(t, m.ScoreValue)
		assert.Equal(t, 0.80, *m.ScoreValue)
		assert.Equal(t, "the answer covered the central finding", m.Reasoning)
		assert.Equal(t, "pass", m.Assessment)
		assert.Equal(t, "0.80", m.EvalMetricMetadata["anchor"])
		// When the evaluator sets per-evaluation Tags, the metric carries
		// only those tags — matches Python parity. Experiment-level
		// identifiers (experiment_id, run_id, dataset_id, ...) live on
		// the experiment span and on the dedicated ExperimentID field of
		// the wire struct, so they're not duplicated here.
		assert.Equal(t, []string{"axis:summary_findings"}, m.Tags)
	}
	require.True(t, found, "did not find a rich-judge metric in the captured events")
}

// TestEvaluatorResult_NilRichResultFallsThrough asserts that returning
// (*EvaluatorResult)(nil) does not panic and the resulting Evaluation
// has zero-value Value (and zero-value optional fields). This is the
// natural behavior when an evaluator typed as returning *EvaluatorResult
// has a nil shortcut on error.
func TestEvaluatorResult_NilRichResultFallsThrough(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	evs := []experiment.Evaluator{
		experiment.NewEvaluator("nil-rich", func(_ context.Context, _ dataset.Record, _ any) (any, error) {
			var r *experiment.EvaluatorResult
			return r, nil
		}),
	}
	exp, err := experiment.New("evaluator-nil-rich", createTestTask(), createTestDataset(t), evs,
		experiment.WithProjectName("test-project"))
	require.NoError(t, err)

	res, err := exp.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, res.Runs, 1)
	for _, r := range res.Runs[0].Results {
		require.Len(t, r.Evaluations, 1)
		ev := r.Evaluations[0]
		assert.Empty(t, ev.Reasoning)
		assert.Empty(t, ev.Assessment)
		assert.Empty(t, ev.Metadata)
		assert.Empty(t, ev.Tags)
	}
}

// TestEvaluatorResult_MixedLegacyAndRich asserts a single experiment
// can mix evaluators returning bare values and evaluators returning
// *EvaluatorResult.
func TestEvaluatorResult_MixedLegacyAndRich(t *testing.T) {
	var (
		events []llmobstransport.ExperimentEvalMetricEvent
		mu     sync.Mutex
	)
	tt := testTracer(t, testtracer.WithMockResponses(captureExperimentEvents(&events, &mu)))
	defer tt.Stop()

	evs := []experiment.Evaluator{
		experiment.NewEvaluator("legacy", func(_ context.Context, _ dataset.Record, _ any) (any, error) {
			return true, nil
		}),
		experiment.NewEvaluator("rich", func(_ context.Context, _ dataset.Record, _ any) (any, error) {
			return &experiment.EvaluatorResult{
				Value:     "good",
				Reasoning: "categorical with reasoning",
			}, nil
		}),
	}
	exp, err := experiment.New("evaluator-mixed", createTestTask(), createTestDataset(t), evs,
		experiment.WithProjectName("test-project"))
	require.NoError(t, err)

	_, err = exp.Run(context.Background())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	var legacy, rich *llmobstransport.ExperimentEvalMetricEvent
	for i := range events {
		switch events[i].Label {
		case "legacy":
			legacy = &events[i]
		case "rich":
			rich = &events[i]
		}
	}
	require.NotNil(t, legacy, "did not find legacy metric")
	require.NotNil(t, rich, "did not find rich metric")
	assert.Equal(t, "boolean", legacy.MetricType)
	assert.Empty(t, legacy.Reasoning)
	assert.Equal(t, "categorical", rich.MetricType)
	require.NotNil(t, rich.CategoricalValue)
	assert.Equal(t, "good", *rich.CategoricalValue)
	assert.Equal(t, "categorical with reasoning", rich.Reasoning)
}
