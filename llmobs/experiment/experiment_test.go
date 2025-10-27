// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment_test

import (
	"bytes"
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
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
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
		tt := testTracer(t)
		defer tt.Stop()

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
		tt := testTracer(t)
		defer tt.Stop()

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

		// Use agentless mode to trigger app key requirement
		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsAgentlessEnabled(true)))
		defer tt.Stop()

		_, err := experiment.New(
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
		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsAgentlessEnabled(false)))
		defer tt.Stop()

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

		tt := testTracer(t)
		defer tt.Stop()

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
		tt := testTracer(t, testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsProjectName("tracer-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		))
		defer tt.Stop()

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
		tt := testTracer(t, testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsProjectName("tracer-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		))
		defer tt.Stop()

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
		h := func(r *http.Request) *http.Response {
			// Normalize URL by trimming evp_proxy prefix if present
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")

			// Capture headers from experiment-related requests
			if strings.Contains(path, "/api/unstable/llm-obs/v1/projects") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default experiment mock handler handle the response
			return createMockHandler()(r)
		}

		// Force agentless mode explicitly
		tt := testTracer(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsAgentlessEnabled(true),
			),
			testtracer.WithMockResponses(h),
		)
		defer tt.Stop()

		ds := createTestDataset(t)
		task := createTestTask()
		evaluators := createTestEvaluators()

		exp, err := experiment.New(
			"test-experiment-agentless-header",
			task,
			ds,
			evaluators,
			experiment.WithDescription("Test DD_APP_KEY header in agentless mode"),
			experiment.WithProjectName("test-project"),
		)
		require.NoError(t, err)

		// Run experiment to trigger project creation request
		_, err = exp.Run(context.Background())
		require.NoError(t, err)

		// Verify DD-APPLICATION-KEY header was set
		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, testAppKey, capturedHeaders.Get("DD-APPLICATION-KEY"), "DD-APPLICATION-KEY header should be set in agentless mode")
	})
	t.Run("dd-app-key-header-agent-mode", func(t *testing.T) {

		var capturedHeaders http.Header
		h := func(r *http.Request) *http.Response {
			// Normalize URL by trimming evp_proxy prefix if present
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")

			// Capture headers from experiment-related requests
			if strings.Contains(path, "/api/unstable/llm-obs/v1/projects") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default experiment mock handler handle the response
			return createMockHandler()(r)
		}

		// Force agent mode explicitly
		tt := testTracer(t,
			testtracer.WithTracerStartOpts(
				tracer.WithLLMObsAgentlessEnabled(false),
			),
			testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
				Endpoints: []string{"/evp_proxy/v2/"}, // Agent supports evp_proxy
			}),
			testtracer.WithMockResponses(h),
		)
		defer tt.Stop()

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
		tt := testTracer(t)
		defer tt.Stop()

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
		spans := tt.WaitForLLMObsSpans(t, 2) // One span per dataset record
		require.Len(t, spans, 2)
		for _, span := range spans {
			assert.Equal(t, "test-task", span.Name)
			assert.Equal(t, "experiment", span.Meta["span.kind"])
		}
	})
	t.Run("run-with-options", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

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
		spans := tt.WaitForLLMObsSpans(t, 1)
		require.Len(t, spans, 1)
	})
	t.Run("task-error-handling", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

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

	t.Run("evaluator-error-handling", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

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

func TestExperimentURL(t *testing.T) {
	run := func(t *testing.T) string {
		tt := testTracer(t)
		defer tt.Stop()

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
	tt := testTracer(t)
	defer tt.Stop()

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

func testTracer(t *testing.T, opts ...testtracer.Option) *testtracer.TestTracer {
	defaultOpts := []testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-app"),
			tracer.WithLLMObsAgentlessEnabled(false),
			tracer.WithLLMObsProjectName("test-project"),
			tracer.WithService("test-service"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
		testtracer.WithMockResponses(createMockHandler()),
	}
	allOpts := append(defaultOpts, opts...)
	tt := testtracer.Start(t, allOpts...)
	t.Cleanup(tt.Stop)
	return tt
}

// createMockHandler creates a mock handler for experiment-related requests (both agent and agentless)
func createMockHandler() testtracer.MockResponseFunc {
	return func(r *http.Request) *http.Response {
		path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")

		switch {
		case path == "/api/unstable/llm-obs/v1/projects":
			return handleMockProjects(r)
		case path == "/api/unstable/llm-obs/v1/experiments":
			return handleMockExperiments(r)
		case strings.HasPrefix(path, "/api/unstable/llm-obs/v1/experiments/") && strings.HasSuffix(path, "/events"):
			return handleMockExperimentEvents(r)
		case strings.Contains(path, "/datasets") && strings.HasSuffix(path, "/batch_update"):
			return handleMockDatasetBatchUpdate(r)
		case strings.Contains(path, "/datasets"):
			return handleMockDatasets(r)
		default:
			return nil
		}
	}
}

func handleMockProjects(r *http.Request) *http.Response {
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
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respData)),
		Request:    r,
	}
}

func handleMockExperiments(r *http.Request) *http.Response {
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
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respData)),
		Request:    r,
	}
}

func handleMockExperimentEvents(r *http.Request) *http.Response {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    r,
	}
}

func handleMockDatasets(r *http.Request) *http.Response {
	if r.Method == http.MethodGet {
		// Return empty list for "not found"
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data": []}`)),
			Request:    r,
		}
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
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(respData)),
			Request:    r,
		}
	}
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    r,
	}
}

func handleMockDatasetBatchUpdate(r *http.Request) *http.Response {
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
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respData)),
		Request:    r,
	}
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
