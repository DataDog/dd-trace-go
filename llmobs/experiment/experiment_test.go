// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/llmobstest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

const (
	testAPIKey = "abcd1234efgh5678ijkl9012mnop3456"
	testAppKey = "test-app-key"
)

func TestExperimentCreation(t *testing.T) {
	t.Run("successful-creation", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment description"),
			experiment.WithProjectName("test-project"),
			experiment.WithTags(map[string]string{"env": "test"}),
			experiment.WithExperimentConfig(map[string]any{"model": "test-model"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, exp)
		assert.Equal(t, "test-experiment", exp.Name)
	})
	t.Run("missing-project-name", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		_, err := experiment.New(
			"test-experiment",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment description"),
			experiment.WithProjectName(""),
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "project name must be provided")
	})
	t.Run("missing-dd-app-key-agentless", func(t *testing.T) {
		t.Setenv("DD_API_KEY", testAPIKey)
		// DD_APP_KEY is mandatory for experiments in agentless mode
		t.Setenv("DD_APP_KEY", "")

		// Use agentless mode to trigger app key requirement.
		// Note: coll.TracerOption() forces ResolvedAgentlessEnabled=false, so we
		// intentionally skip it here to allow true agentless mode validation.
		agent, err := tracertest.StartAgent(t)
		require.NoError(t, err)
		_, err = tracertest.Start(t, agent,
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsAgentlessEnabled(true),
			tracer.WithLLMObsProjectName("test-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		)
		require.NoError(t, err)

		_, err = experiment.New(
			"test-experiment",
			nil,
			nil,
			nil,
			experiment.WithDescription("Test experiment description"),
			experiment.WithProjectName("test-project"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "an app key must be provided")
	})
	t.Run("missing-dd-app-key-agent-mode", func(t *testing.T) {
		// DD_APP_KEY is not required in agent mode
		t.Setenv("DD_APP_KEY", "")

		// Use agent mode - app key should not be required
		testTracer(t, tracer.WithLLMObsAgentlessEnabled(false))

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment description"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)
		assert.NotNil(t, exp)
		assert.Equal(t, "test-experiment", exp.Name)
	})
	t.Run("project-name-from-env-variable", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_PROJECT_NAME", "env-project")

		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		// Test that project name comes from environment variable
		exp, err := experiment.New(
			"test-experiment-env-project",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test with env project name"),
			// No explicit project name - should use DD_LLMOBS_PROJECT_NAME
		)
		require.NoError(t, err)
		assert.NotNil(t, exp)
		assert.Equal(t, "test-experiment-env-project", exp.Name)
	})
	t.Run("project-name-from-tracer-option", func(t *testing.T) {

		// Use tracer option to set project name globally
		testTracer(t,
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsProjectName("tracer-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		// Test that project name comes from tracer option
		exp, err := experiment.New(
			"test-experiment-tracer-project",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test with tracer project name"),
			// No explicit project name - should use tracer.WithLLMObsProjectName
		)
		require.NoError(t, err)
		assert.NotNil(t, exp)
		assert.Equal(t, "test-experiment-tracer-project", exp.Name)
	})
	t.Run("project-name-precedence", func(t *testing.T) {
		t.Setenv("DD_LLMOBS_PROJECT_NAME", "env-project")

		// Use tracer option to set project name globally
		testTracer(t,
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsProjectName("tracer-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		// Test that explicit option takes precedence over env var and tracer option
		exp, err := experiment.New(
			"test-experiment-precedence",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test project name precedence"),
			experiment.WithProjectName("explicit-project"), // Should override env and tracer
		)
		require.NoError(t, err)
		assert.NotNil(t, exp)
		assert.Equal(t, "test-experiment-precedence", exp.Name)
	})
}

func TestDDAppKeyHeader(t *testing.T) {
	t.Run("dd-app-key-header-agentless", func(t *testing.T) {
		t.Setenv("DD_API_KEY", testAPIKey)
		t.Setenv("DD_APP_KEY", testAppKey)

		var capturedHeaders http.Header

		agent, err := tracertest.StartAgent(t)
		require.NoError(t, err)
		coll := llmobstest.New(t)
		coll.HandleFunc("/api/unstable/llm-obs/v1/", func(w http.ResponseWriter, r *http.Request) {
			// Capture headers from experiment-related requests
			if strings.Contains(r.URL.Path, "/api/unstable/llm-obs/v1/projects") {
				capturedHeaders = r.Header.Clone()
			}
			createMockHandler()(w, r)
		})

		// Note: coll.TracerOption() sets testBaseURL which forces ResolvedAgentlessEnabled=false.
		// In the coll-based setup all requests go through the collector in agent mode.
		_, err = tracertest.Start(t, agent,
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsProjectName("test-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
			coll.TracerOption(),
		)
		require.NoError(t, err)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-header",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test request header handling"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		// Run experiment to trigger project creation request
		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		// Verify X-Datadog-NeedsAppKey header is set in coll-based (agent) mode
		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, "true", capturedHeaders.Get("X-Datadog-NeedsAppKey"), "X-Datadog-NeedsAppKey header should be set")
	})
	t.Run("dd-app-key-header-agent-mode", func(t *testing.T) {

		var capturedHeaders http.Header
		agent, err := tracertest.StartAgent(t)
		require.NoError(t, err)
		coll := llmobstest.New(t)
		coll.HandleFunc("/api/unstable/llm-obs/v1/", func(w http.ResponseWriter, r *http.Request) {
			// Capture headers from experiment-related requests
			if strings.Contains(r.URL.Path, "/api/unstable/llm-obs/v1/projects") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default experiment mock handler handle the response
			createMockHandler()(w, r)
		})

		_, err = tracertest.Start(t, agent,
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsAgentlessEnabled(false),
			tracer.WithLLMObsProjectName("test-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
			coll.TracerOption(),
		)
		require.NoError(t, err)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-agent-header",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test DD_APP_KEY header in agent mode"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		// Run experiment to trigger project creation request
		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		// Verify X-Datadog-NeedsAppKey header is set in agent mode (app key is ignored)
		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, "true", capturedHeaders.Get("X-Datadog-NeedsAppKey"), "X-Datadog-NeedsAppKey header should always be set in agent mode")
		assert.Empty(t, capturedHeaders.Get("DD-APPLICATION-KEY"), "DD-APPLICATION-KEY header should not be set in agent mode")
	})
}

func TestExperimentRun(t *testing.T) {
	t.Run("successful-run", func(t *testing.T) {
		coll := testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment description"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background())
		require.NoError(t, err)

		// Verify results
		assert.Len(t, results.Results, 2) // Our test dataset has 2 records

		for _, result := range results.Results {
			assert.NotEmpty(t, result.SpanID)
			assert.NotEmpty(t, result.TraceID)
			assert.NotZero(t, result.Timestamp)
			assert.NotNil(t, result.Record.Input)
			assert.NotNil(t, result.Output)
			assert.NotNil(t, result.Record.ExpectedOutput)
			assert.Len(t, result.Evaluations, 2) // We have 2 evaluators
			assert.NoError(t, result.Error)

			// Check evaluations
			for _, eval := range result.Evaluations {
				assert.NotEmpty(t, eval.Name)
				assert.NotNil(t, eval.Value)
				assert.NoError(t, eval.Error)
			}
		}

		// Verify experiment spans were created
		tracer.Flush()
		require.Equal(t, 2, coll.SpanCount()) // One span per dataset record
		span := coll.RequireSpan(t, "test-task")
		assert.Equal(t, "experiment", span.Meta["span.kind"])
	})
	t.Run("run-with-options", func(t *testing.T) {
		coll := testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-options",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment with options"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background(),
			experiment.WithMaxConcurrency(1),
			experiment.WithSampleSize(1),
			experiment.WithAbortOnError(false),
		)
		require.NoError(t, err)

		// Should only have 1 result due to sample size
		assert.Len(t, results.Results, 1)
		assert.NotNil(t, results.Results[0].Record)

		// Verify only 1 span was created
		tracer.Flush()
		require.Equal(t, 1, coll.SpanCount())
	})
	t.Run("task-error-handling", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)

		// Create a task that always fails
		task := experiment.NewTask("failing-task", func(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error) {
			return nil, errors.New("task failed")
		})
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-task-error",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment with task errors"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background(),
			experiment.WithAbortOnError(false), // Don't abort on errors
		)
		require.NoError(t, err)

		// Should have results but with errors
		assert.Len(t, results.Results, 2)
		for _, result := range results.Results {
			if assert.Error(t, result.Error) {
				assert.Contains(t, result.Error.Error(), "task failed")
			}
		}
	})

	t.Run("task-error-propagated-to-span", func(t *testing.T) {
		coll := testTracer(t)

		ds := createTestDataset(t)
		taskErr := errors.New("task failed")
		task := experiment.NewTask("failing-task", func(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error) {
			return nil, taskErr
		})

		exp, err := experiment.New(
			"test-experiment-span-error",
			task,
			ds,
			nil,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background(), experiment.WithAbortOnError(false))
		require.NoError(t, err)

		tracer.Flush()
		require.Equal(t, 2, coll.SpanCount())
		for i := range 2 {
			span := coll.RequireSpan(t, "failing-task")
			assert.Equal(t, "error", span.Status, "span %d status should be 'error' when task fails", i)
		}
	})

	t.Run("evaluator-error-handling", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()

		// Create evaluators where one always fails
		evaluators := []experiment.Evaluator{
			experiment.NewEvaluator("working-evaluator", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
				return "success", nil
			}),
			experiment.NewEvaluator("failing-evaluator", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
				return nil, errors.New("evaluator failed")
			}),
		}

		exp, err := experiment.New(
			"test-experiment-evaluator-error",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment with evaluator errors"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background(),
			experiment.WithAbortOnError(false), // Don't abort on errors
		)
		require.NoError(t, err)

		// Should have results
		assert.Len(t, results.Results, 2)
		for _, result := range results.Results {
			assert.NoError(t, result.Error) // Task should succeed
			assert.Len(t, result.Evaluations, 2)

			// First evaluator should succeed
			assert.Equal(t, "working-evaluator", result.Evaluations[0].Name)
			assert.Equal(t, "success", result.Evaluations[0].Value)
			assert.NoError(t, result.Evaluations[0].Error)

			// Second evaluator should fail
			assert.Equal(t, "failing-evaluator", result.Evaluations[1].Name)
			assert.Error(t, result.Evaluations[1].Error)
			assert.Contains(t, result.Evaluations[1].Error.Error(), "evaluator failed")
		}
	})
}

func TestExperimentMultiRun(t *testing.T) {
	t.Run("multiple-runs-produce-separate-run-results", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-multi-run",
			task,
			ds,
			evaluators,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(3),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background())
		require.NoError(t, err)

		// Should have 3 runs
		require.Len(t, results.Runs, 3)

		// Each run should have a unique ID and the correct iteration number
		seenIDs := make(map[string]bool)
		for i, run := range results.Runs {
			assert.NotEmpty(t, run.Run.ID, "run ID should be set")
			assert.Equal(t, i+1, run.Run.Iteration, "run iteration should be 1-indexed")
			assert.False(t, seenIDs[run.Run.ID], "run ID should be unique across runs")
			seenIDs[run.Run.ID] = true

			// Each run should have results for all dataset records
			assert.Len(t, run.Results, 2)
		}

		// Backward compat: Results and SummaryEvaluations point to first run
		assert.Equal(t, results.Runs[0].Results, results.Results)
		assert.Equal(t, results.Runs[0].SummaryEvaluations, results.SummaryEvaluations)
	})

	t.Run("single-run-default-backward-compat", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-single-run",
			task,
			ds,
			evaluators,
			experiment.WithProjectName("test-project"),
			// No WithRuns — defaults to 1
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background())
		require.NoError(t, err)

		require.Len(t, results.Runs, 1)
		assert.Equal(t, 1, results.Runs[0].Run.Iteration)
		assert.NotEmpty(t, results.Runs[0].Run.ID)

		// Legacy fields still populated
		assert.Len(t, results.Results, 2)
	})

	t.Run("spans-carry-run-id-and-run-iteration-tags", func(t *testing.T) {
		coll := testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()

		exp, err := experiment.New(
			"test-experiment-span-tags",
			task,
			ds,
			nil,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(2),
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background())
		require.NoError(t, err)
		require.Len(t, results.Runs, 2)

		// 2 runs × 2 records = 4 spans produced
		tracer.Flush()
		require.Equal(t, 4, coll.SpanCount())

		// Verify tags are present on spans — run_id/iteration distinctness is
		// already covered by the results.Runs assertions above.
		span := coll.RequireSpan(t, "test-task")
		assert.NotEmpty(t, spanTagValue(span.Tags, "run_id"), "span should have run_id tag")
		assert.NotEmpty(t, spanTagValue(span.Tags, "run_iteration"), "span should have run_iteration tag")
	})

	t.Run("push-events-called-once-per-run", func(t *testing.T) {
		var pushCount int
		testTracerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/events") {
				pushCount++
			}
			createMockHandler()(w, r)
		})

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		const numRuns = 3
		exp, err := experiment.New(
			"test-experiment-push-count",
			task,
			ds,
			evaluators,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(numRuns),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		assert.Equal(t, numRuns, pushCount, "PushExperimentEvents should be called once per run")
	})

	t.Run("run-count-sent-to-backend", func(t *testing.T) {
		var capturedRunCount int
		testTracerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/unstable/llm-obs/v1/experiments" && r.Method == http.MethodPost {
				var body struct {
					Data struct {
						Attributes struct {
							RunCount int `json:"run_count"`
						} `json:"attributes"`
					} `json:"data"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
					capturedRunCount = body.Data.Attributes.RunCount
				}
			}
			createMockHandler()(w, r)
		})

		ds := createTestDataset(t)
		task := createTestTask()

		exp, err := experiment.New(
			"test-experiment-run-count",
			task,
			ds,
			nil,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(4),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		assert.Equal(t, 4, capturedRunCount, "run_count should be sent to the backend")
	})

	t.Run("invalid-runs-value-ignored", func(t *testing.T) {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()

		exp, err := experiment.New(
			"test-experiment-invalid-runs",
			task,
			ds,
			nil,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(0), // should be ignored, defaulting to 1
		)
		require.NoError(t, err)

		results, err := exp.Run(context.Background())
		require.NoError(t, err)
		assert.Len(t, results.Runs, 1)
	})
}

func TestExperimentURL(t *testing.T) {
	run := func(t *testing.T) string {
		testTracer(t)

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-url",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test experiment URL"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		// Run the experiment to get an ID
		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		return exp.URL()
	}
	t.Run("with-dd-site", func(t *testing.T) {
		t.Setenv("DD_SITE", "my-dd-site")
		url := run(t)
		assert.Equal(t, "https://my-dd-site/llm/experiments/test-experiment-id", url)
	})
	t.Run("empty-dd-site", func(t *testing.T) {
		url := run(t)
		assert.Equal(t, "https://app.datadoghq.com/llm/experiments/test-experiment-id", url)
	})
}

func TestExperimentMetricGeneration(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)

	// Create evaluators that return different metric types
	evaluators := []experiment.Evaluator{
		experiment.NewEvaluator("categorical-eval", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return "excellent", nil
		}),
		experiment.NewEvaluator("score-eval", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return 0.95, nil
		}),
		experiment.NewEvaluator("boolean-eval", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return true, nil
		}),
		experiment.NewEvaluator("int-eval", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return 42, nil
		}),
	}

	task := createTestTask()

	exp, err := experiment.New(
		"test-experiment-metrics",
		task,
		ds,
		evaluators,
		experiment.WithDescription("Test experiment metric types"),
		experiment.WithProjectName("test-project"),
	)
	require.NoError(t, err)

	results, err := exp.Run(context.Background())
	require.NoError(t, err)

	// Verify results contain evaluations with different value types
	require.Len(t, results.Results, 2) // 2 dataset records
	for _, result := range results.Results {
		assert.Len(t, result.Evaluations, 4) // 4 evaluators

		// Check that evaluations have the expected values
		evalsByName := make(map[string]*experiment.Evaluation)
		for _, eval := range result.Evaluations {
			evalsByName[eval.Name] = eval
		}

		assert.Equal(t, "excellent", evalsByName["categorical-eval"].Value)
		assert.Equal(t, 0.95, evalsByName["score-eval"].Value)
		assert.Equal(t, true, evalsByName["boolean-eval"].Value)
		assert.Equal(t, 42, evalsByName["int-eval"].Value)
	}
}

// Helper functions

func testTracer(t *testing.T, tracerOpts ...tracer.StartOption) *llmobstest.Collector {
	t.Helper()
	coll := llmobstest.New(t)
	registerMockHandlers(coll)
	_, _, err := tracertest.Bootstrap(t, append([]tracer.StartOption{
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("test-app"),
		tracer.WithLLMObsAgentlessEnabled(false),
		tracer.WithLLMObsProjectName("test-project"),
		tracer.WithService("test-service"),
		tracer.WithLogStartup(false),
		coll.TracerOption(),
	}, tracerOpts...)...)
	require.NoError(t, err)
	return coll
}

// testTracerWithHandler sets up the tracer with a custom handler for
// /api/unstable/llm-obs/v1/ instead of the default mock. Use this when you
// need to intercept specific API calls (e.g. status updates) while still
// delegating unmatched paths to createMockHandler.
func testTracerWithHandler(t *testing.T, handler http.HandlerFunc) *llmobstest.Collector {
	t.Helper()
	coll := llmobstest.New(t)
	coll.HandleFunc("/api/unstable/llm-obs/v1/", handler)
	_, _, err := tracertest.Bootstrap(t,
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("test-app"),
		tracer.WithLLMObsAgentlessEnabled(false),
		tracer.WithLLMObsProjectName("test-project"),
		tracer.WithService("test-service"),
		tracer.WithLogStartup(false),
		coll.TracerOption(),
	)
	require.NoError(t, err)
	return coll
}

func registerMockHandlers(coll *llmobstest.Collector) {
	coll.HandleFunc("/api/unstable/llm-obs/v1/", createMockHandler())
}

// createMockHandler creates a mock handler for experiment-related requests (both agent and agentless)
func createMockHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case path == "/api/unstable/llm-obs/v1/projects":
			handleMockProjects(w, r)
		case path == "/api/unstable/llm-obs/v1/experiments" && r.Method == http.MethodPost:
			handleMockExperiments(w, r)
		case strings.HasPrefix(path, "/api/unstable/llm-obs/v1/experiments/") && strings.HasSuffix(path, "/events"):
			handleMockExperimentEvents(w, r)
		case strings.HasPrefix(path, "/api/unstable/llm-obs/v1/experiments/") && r.Method == http.MethodPatch:
			handleMockExperimentStatusUpdate(w, r)
		case strings.Contains(path, "/datasets") && strings.HasSuffix(path, "/batch_update"):
			handleMockDatasetBatchUpdate(w, r)
		case strings.Contains(path, "/datasets"):
			handleMockDatasets(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func handleMockProjects(w http.ResponseWriter, _ *http.Request) {
	response := llmobstransport.CreateProjectResponse{
		Data: llmobstransport.ResponseData[llmobstransport.ProjectView]{
			ID:   "test-project-id",
			Type: "projects",
			Attributes: llmobstransport.ProjectView{
				ID:   "test-project-id",
				Name: "test-project",
			},
		},
	}
	respData, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

func handleMockExperiments(w http.ResponseWriter, _ *http.Request) {
	response := llmobstransport.CreateExperimentResponse{
		Data: llmobstransport.ResponseData[llmobstransport.ExperimentView]{
			ID:   "test-experiment-id",
			Type: "experiments",
			Attributes: llmobstransport.ExperimentView{
				ID:             "test-experiment-id",
				ProjectID:      "test-project-id",
				DatasetID:      "test-dataset-id",
				Name:           "test-experiment",
				Description:    "Test experiment description",
				DatasetVersion: 1,
				EnsureUnique:   true,
			},
		},
	}
	respData, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

func handleMockExperimentStatusUpdate(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{}`))
}

func handleMockExperimentEvents(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{}`))
}

func handleMockDatasets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Return empty list for "not found"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": []}`))
		return
	}
	if r.Method == http.MethodPost {
		response := llmobstransport.CreateDatasetResponse{
			Data: llmobstransport.ResponseData[llmobstransport.DatasetView]{
				ID:   "test-dataset-id",
				Type: "datasets",
				Attributes: llmobstransport.DatasetView{
					ID:             "test-dataset-id",
					Name:           "test-dataset",
					Description:    "Test dataset for experiments",
					CurrentVersion: 1,
				},
			},
		}
		respData, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(respData)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{}`))
}

func handleMockDatasetBatchUpdate(w http.ResponseWriter, _ *http.Request) {
	response := llmobstransport.BatchUpdateDatasetResponse{
		Data: []llmobstransport.ResponseData[llmobstransport.DatasetRecordView]{
			{
				ID:   "test-record-id-0",
				Type: "dataset_records",
				Attributes: llmobstransport.DatasetRecordView{
					ID:             "test-record-id-0",
					Input:          map[string]any{"question": "What is the capital of France?"},
					ExpectedOutput: "Paris",
					Version:        1,
				},
			},
			{
				ID:   "test-record-id-1",
				Type: "dataset_records",
				Attributes: llmobstransport.DatasetRecordView{
					ID:             "test-record-id-1",
					Input:          map[string]any{"question": "What is the capital of Germany?"},
					ExpectedOutput: "Berlin",
					Version:        1,
				},
			},
		},
	}
	respData, _ := json.Marshal(response)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

func createTestDataset(t *testing.T) *dataset.Dataset {
	records := []dataset.Record{
		{
			Input:          map[string]any{"question": "What is the capital of France?"},
			ExpectedOutput: "Paris",
		},
		{
			Input:          map[string]any{"question": "What is the capital of Germany?"},
			ExpectedOutput: "Berlin",
		},
	}

	ds, err := dataset.Create(context.Background(), "test-dataset", records, dataset.WithDescription("Test dataset for experiments"))
	require.NoError(t, err)
	return ds
}

func createTestTask() experiment.Task {
	return experiment.NewTask("test-task", func(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error) {
		inputMap, ok := rec.Input.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("input is not a map")
		}
		question, ok := inputMap["question"].(string)
		if !ok {
			return nil, fmt.Errorf("question not found in input")
		}

		switch question {
		case "What is the capital of France?":
			return "Paris", nil
		case "What is the capital of Germany?":
			return "Berlin", nil
		default:
			return "Unknown", nil
		}
	})
}

// statusUpdate captures a single PATCH status call to the backend.
type statusUpdate struct {
	Status string
	Error  string
}

// captureStatusUpdates returns a mock handler that records every experiment status
// PATCH alongside the default mock handler for all other requests.
func captureStatusUpdates(updates *[]statusUpdate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/api/unstable/llm-obs/v1/experiments/") && r.Method == http.MethodPatch {
			var body struct {
				Data struct {
					Attributes struct {
						Status string `json:"status"`
						Error  string `json:"error"`
					} `json:"attributes"`
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				*updates = append(*updates, statusUpdate{
					Status: body.Data.Attributes.Status,
					Error:  body.Data.Attributes.Error,
				})
			}
			handleMockExperimentStatusUpdate(w, r)
			return
		}
		createMockHandler()(w, r)
	}
}

func TestExperimentStatusUpdates(t *testing.T) {
	t.Run("completed-on-success", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		exp, err := experiment.New(
			"test-status-completed",
			createTestTask(),
			createTestDataset(t),
			createTestEvaluators(),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "completed", updates[1].Status)
		assert.Empty(t, updates[1].Error)
	})

	t.Run("failed-when-task-errors-occur", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		task := experiment.NewTask("failing-task", func(ctx context.Context, rec dataset.Record, _ map[string]any) (any, error) {
			return nil, errors.New("task boom")
		})

		exp, err := experiment.New(
			"test-status-task-failed",
			task,
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background(), experiment.WithAbortOnError(false))
		require.NoError(t, err) // run itself succeeds; errors are in results

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "failed", updates[1].Status)
		assert.Contains(t, updates[1].Error, "task boom")
	})

	t.Run("failed-with-evaluator-errors", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		evaluators := []experiment.Evaluator{
			experiment.NewEvaluator("bad-eval", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
				return nil, errors.New("eval boom")
			}),
		}

		exp, err := experiment.New(
			"test-status-eval-failed",
			createTestTask(),
			createTestDataset(t),
			evaluators,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background(), experiment.WithAbortOnError(false))
		require.NoError(t, err)

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "failed", updates[1].Status)
		assert.Contains(t, updates[1].Error, "bad-eval: eval boom")
	})

	t.Run("failed-with-summary-evaluator-errors", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		exp, err := experiment.New(
			"test-status-sumeval-failed",
			createTestTask(),
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
			experiment.WithSummaryEvaluators(
				experiment.NewSummaryEvaluator("bad-summary", func(ctx context.Context, results []*experiment.RecordResult) (any, error) {
					return nil, errors.New("summary boom")
				}),
			),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background(), experiment.WithAbortOnError(false))
		require.NoError(t, err)

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "failed", updates[1].Status)
		assert.Contains(t, updates[1].Error, "bad-summary: summary boom")
	})

	t.Run("failed-on-abort-error", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		task := experiment.NewTask("failing-task", func(ctx context.Context, rec dataset.Record, _ map[string]any) (any, error) {
			return nil, errors.New("hard abort")
		})

		exp, err := experiment.New(
			"test-status-abort",
			task,
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background(), experiment.WithAbortOnError(true))
		require.Error(t, err)

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "failed", updates[1].Status)
		assert.Contains(t, updates[1].Error, "hard abort")
	})

	t.Run("interrupted-on-context-cancellation", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel the context as soon as the task starts running.
		task := experiment.NewTask("cancelling-task", func(ctx context.Context, rec dataset.Record, _ map[string]any) (any, error) {
			cancel()
			return nil, ctx.Err()
		})

		exp, err := experiment.New(
			"test-status-interrupted",
			task,
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(ctx, experiment.WithAbortOnError(true))
		require.Error(t, err)

		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "interrupted", updates[1].Status)
	})

	t.Run("no-status-sent-if-experiment-creation-fails", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/unstable/llm-obs/v1/experiments" && r.Method == http.MethodPost {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"backend unavailable"}`))
				return
			}
			captureStatusUpdates(&updates)(w, r)
		})

		exp, err := experiment.New(
			"test-status-no-creation",
			createTestTask(),
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background())
		require.Error(t, err)

		assert.Empty(t, updates, "no status updates should be sent if experiment creation fails")
	})

	t.Run("running-sent-once-for-multi-run", func(t *testing.T) {
		var updates []statusUpdate
		testTracerWithHandler(t, captureStatusUpdates(&updates))

		exp, err := experiment.New(
			"test-status-multi-run",
			createTestTask(),
			createTestDataset(t),
			nil,
			experiment.WithProjectName("test-project"),
			experiment.WithRuns(3),
		)
		require.NoError(t, err)

		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		// Status is experiment-level, not per-run: exactly one "running" and one "completed".
		require.Len(t, updates, 2)
		assert.Equal(t, "running", updates[0].Status)
		assert.Equal(t, "completed", updates[1].Status)
	})
}

// spanTagValue extracts the value for key from a []string tag slice where entries
// are formatted as "key:value".
func spanTagValue(tags []string, key string) string {
	prefix := key + ":"
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return t[len(prefix):]
		}
	}
	return ""
}

func createTestEvaluators() []experiment.Evaluator {
	return []experiment.Evaluator{
		experiment.NewEvaluator("exact-match", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			return output == rec.ExpectedOutput, nil
		}),
		experiment.NewEvaluator("similarity", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
			if output == rec.ExpectedOutput {
				return 1.0, nil
			}
			return 0.5, nil
		}),
	}
}

// TestExperimentLargeDatasetSizeBasedFlushing verifies that span events are flushed in multiple
// batches when the cumulative payload size exceeds the backend limit. Without size-based flushing,
// all span events accumulate between periodic flushes and are sent as a single oversized request.
func TestExperimentLargeDatasetSizeBasedFlushing(t *testing.T) {
	// numRecords is chosen so that the total span payload exceeds the 5MB per-batch limit,
	// forcing at least one early flush. A small count is used to keep the test fast.
	const numRecords = 12

	coll := llmobstest.New(t)
	coll.HandleFunc("/api/unstable/llm-obs/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Return exactly as many entries as the request sent so that dataset.Push
		// succeeds (transport validates response count == insert+update count).
		if strings.Contains(path, "/datasets") && strings.HasSuffix(path, "/batch_update") {
			body, _ := io.ReadAll(r.Body)
			var req llmobstransport.BatchUpdateDatasetRequest
			_ = json.Unmarshal(body, &req)
			count := len(req.Data.Attributes.InsertRecords) + len(req.Data.Attributes.UpdateRecords)
			if count == 0 {
				count = numRecords // fallback for safety
			}
			data := make([]llmobstransport.ResponseData[llmobstransport.DatasetRecordView], count)
			for i := range data {
				id := fmt.Sprintf("record-id-%d", i)
				data[i] = llmobstransport.ResponseData[llmobstransport.DatasetRecordView]{
					ID:         id,
					Type:       "dataset_records",
					Attributes: llmobstransport.DatasetRecordView{ID: id, Version: 1},
				}
			}
			resp := llmobstransport.BatchUpdateDatasetResponse{Data: data}
			b, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(b)
			return
		}

		createMockHandler()(w, r)
	}))
	_, _, err := tracertest.Bootstrap(t,
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("test-app"),
		tracer.WithLLMObsAgentlessEnabled(false),
		tracer.WithLLMObsProjectName("test-project"),
		tracer.WithService("test-service"),
		tracer.WithLogStartup(false),
		coll.TracerOption(),
	)
	require.NoError(t, err)

	// Each record carries large input and expected output fields so that the combined span
	// payload across all records exceeds the 5MB limit, triggering size-based flushing.
	largeContext := strings.Repeat("x", 200_000)  // ~200KB input field
	largeExpected := strings.Repeat("z", 100_000) // ~100KB expected output field

	records := make([]dataset.Record, numRecords)
	for i := range records {
		records[i] = dataset.Record{
			Input: map[string]any{
				"context":  largeContext,
				"question": fmt.Sprintf("What is the answer for record %d?", i),
			},
			ExpectedOutput: largeExpected,
		}
	}

	ds, err := dataset.Create(context.Background(), "large-dataset", records, dataset.WithProjectName("test-project"))
	require.NoError(t, err)

	// Task produces a large output so that each span payload is substantial (~500KB total).
	task := experiment.NewTask("summarizer", func(_ context.Context, _ dataset.Record, _ map[string]any) (any, error) {
		return strings.Repeat("y", 200_000), nil
	})

	exp, err := experiment.New("large-experiment", task, ds, nil, experiment.WithProjectName("test-project"))
	require.NoError(t, err)

	_, err = exp.Run(context.Background())
	require.NoError(t, err)

	tracer.Flush()
	require.Equal(t, numRecords, coll.SpanCount())

	sizes := coll.SpanBatchSizes()
	require.NotEmpty(t, sizes, "expected at least one HTTP request to the LLMObs endpoint")
	for _, size := range sizes {
		assert.LessOrEqual(t, size, 5_000_000,
			"HTTP batch payload (%d bytes) exceeds the 5MB limit; without size-based flushing, "+
				"all experiment spans accumulate in a single oversized batch", size)
	}
}
