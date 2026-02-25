// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestRegistry(t *testing.T) {
	defs := []*ExperimentDefinition{
		{Name: "exp-a", Description: "Experiment A"},
		{Name: "exp-b", Description: "Experiment B"},
	}
	reg := NewRegistry(defs)

	t.Run("get-existing", func(t *testing.T) {
		def, ok := reg.Get("exp-a")
		require.True(t, ok)
		assert.Equal(t, "Experiment A", def.Description)
	})

	t.Run("get-missing", func(t *testing.T) {
		_, ok := reg.Get("exp-z")
		assert.False(t, ok)
	})

	t.Run("list", func(t *testing.T) {
		all := reg.List()
		assert.Len(t, all, 2)
		assert.Contains(t, all, "exp-a")
		assert.Contains(t, all, "exp-b")
	})
}

func TestListHandler(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()
	evaluators := createTestEvaluators()

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		Description: "A test experiment",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  evaluators,
		Config: map[string]*ConfigField{
			"model":       {Type: ConfigFieldString, Default: "gpt-4", Description: "LLM model"},
			"temperature": {Type: ConfigFieldNumber, Default: 0.7, Description: "Sampling temperature"},
		},
		Tags: map[string]string{"env": "test"},
	}}
	registry := NewRegistry(defs)
	handler := NewListHandler(registry)

	t.Run("success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/list", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var resp map[string][]listExperimentView
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)

		exps := resp["experiments"]
		require.Len(t, exps, 1)
		assert.Equal(t, "test-exp", exps[0].Name)
		assert.Equal(t, "A test experiment", exps[0].Description)
		assert.Equal(t, "test-project", exps[0].ProjectName)
		assert.Equal(t, "test-task", exps[0].TaskName)
		assert.Equal(t, 2, exps[0].DatasetLen)
		assert.ElementsMatch(t, []string{"exact-match", "similarity"}, exps[0].Evaluators)
		require.Len(t, exps[0].Config, 2)
		assert.Equal(t, ConfigFieldString, exps[0].Config["model"].Type)
		assert.Equal(t, "gpt-4", exps[0].Config["model"].Default)
		assert.Equal(t, ConfigFieldNumber, exps[0].Config["temperature"].Type)
	})

	t.Run("method-not-allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/list", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}

func TestEvalHandlerSync(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()
	evaluators := createTestEvaluators()

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		Description: "A test experiment",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  evaluators,
	}}
	registry := NewRegistry(defs)
	handler := NewEvalHandler(registry)

	t.Run("success", func(t *testing.T) {
		body, _ := json.Marshal(EvalRequest{Name: "test-exp", Stream: false})
		req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var resp map[string]any
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "test-exp", resp["experiment_name"])
	})

	t.Run("not-found", func(t *testing.T) {
		body, _ := json.Marshal(EvalRequest{Name: "nonexistent"})
		req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("invalid-json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/eval", strings.NewReader("{bad"))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing-name", func(t *testing.T) {
		body, _ := json.Marshal(EvalRequest{})
		req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("method-not-allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/eval", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}

func TestEvalHandlerStreaming(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()
	evaluators := createTestEvaluators()

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		Description: "A test experiment",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  evaluators,
	}}
	registry := NewRegistry(defs)
	handler := NewEvalHandler(registry)

	body, _ := json.Marshal(EvalRequest{Name: "test-exp", Stream: true, Evaluators: []string{"exact-match", "similarity"}})
	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))

	// Parse all events from the stream
	events := parseStreamEvents(t, rec.Body)
	require.NotEmpty(t, events)

	// First event should be "start"
	assert.Equal(t, "start", events[0].Event)
	startData, ok := events[0].Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test-project", startData["project_name"])
	assert.NotEmpty(t, startData["experiment_id"])

	// Last two events should be "summary" and "done"
	assert.Equal(t, "summary", events[len(events)-2].Event)
	assert.Equal(t, "done", events[len(events)-1].Event)

	// Count progress events — we should have some for each record
	var progressEvents []StreamEvent
	for _, e := range events {
		if e.Event == "progress" {
			progressEvents = append(progressEvents, e)
		}
	}
	// With 2 records, we expect at minimum: running(x2), task_complete(x2), evaluations_complete(x2), success(x2)
	assert.GreaterOrEqual(t, len(progressEvents), 8,
		"expected at least 8 progress events (4 per record), got %d", len(progressEvents))

	// Verify we see all expected statuses
	statusSeen := make(map[string]int)
	for _, pe := range progressEvents {
		data := pe.Data.(map[string]any)
		status := data["status"].(string)
		statusSeen[status]++
	}
	assert.Equal(t, 2, statusSeen["running"])
	assert.Equal(t, 2, statusSeen["task_complete"])
	assert.Equal(t, 2, statusSeen["evaluations_complete"])
	assert.Equal(t, 2, statusSeen["success"])

	// Verify "success" events contain span and eval_metrics
	for _, pe := range progressEvents {
		data := pe.Data.(map[string]any)
		if data["status"] != "success" {
			continue
		}

		// Verify span event is present with expected fields
		span, ok := data["span"].(map[string]any)
		require.True(t, ok, "success event should contain a span object")
		assert.NotEmpty(t, span["span_id"], "span should have span_id")
		assert.NotEmpty(t, span["trace_id"], "span should have trace_id")
		assert.Equal(t, "ok", span["status"])

		meta, ok := span["meta"].(map[string]any)
		require.True(t, ok, "span should have meta")
		assert.Equal(t, "experiment", meta["span.kind"])
		assert.NotNil(t, meta["input"], "span meta should have input")
		assert.NotNil(t, meta["output"], "span meta should have output")
		assert.NotNil(t, meta["expected_output"], "span meta should have expected_output")

		// Verify eval_metrics are present
		evalMetrics, ok := data["eval_metrics"].([]any)
		require.True(t, ok, "success event should contain eval_metrics array")
		assert.Len(t, evalMetrics, 2, "should have 2 eval metrics (exact-match + similarity)")

		for _, em := range evalMetrics {
			metric := em.(map[string]any)
			assert.NotEmpty(t, metric["span_id"])
			assert.NotEmpty(t, metric["trace_id"])
			assert.NotEmpty(t, metric["label"])
			assert.NotEmpty(t, metric["metric_type"])
			assert.NotEmpty(t, metric["experiment_id"])
		}
	}

	// Verify "task_complete" events contain span
	for _, pe := range progressEvents {
		data := pe.Data.(map[string]any)
		if data["status"] != "task_complete" {
			continue
		}
		span, ok := data["span"].(map[string]any)
		require.True(t, ok, "task_complete event should contain a span object")
		assert.NotEmpty(t, span["span_id"])
	}
}

func TestEvalHandlerWithSampleSize(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()
	evaluators := createTestEvaluators()

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  evaluators,
	}}
	registry := NewRegistry(defs)
	handler := NewEvalHandler(registry)

	body, _ := json.Marshal(EvalRequest{
		Name:       "test-exp",
		Stream:     true,
		SampleSize: 1,
		Evaluators: []string{"exact-match", "similarity"},
	})
	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	events := parseStreamEvents(t, rec.Body)
	var progressEvents []StreamEvent
	for _, e := range events {
		if e.Event == "progress" {
			progressEvents = append(progressEvents, e)
		}
	}
	// With 1 record sampled: running, task_complete, evaluations_complete, success = 4
	assert.Equal(t, 4, len(progressEvents),
		"expected 4 progress events for 1 sampled record, got %d", len(progressEvents))
}

func TestEvalHandlerConfigOverride(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)

	// Task that reads config
	var capturedCfg map[string]any
	task := experiment.NewTask("config-task", func(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error) {
		capturedCfg = experimentCfg
		return "result", nil
	})

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  nil,
		Config: map[string]*ConfigField{
			"model":       {Type: ConfigFieldString, Default: "gpt-3.5"},
			"temperature": {Type: ConfigFieldNumber, Default: 0.5},
		},
	}}
	registry := NewRegistry(defs)
	handler := NewEvalHandler(registry)

	body, _ := json.Marshal(EvalRequest{
		Name:   "test-exp",
		Stream: false,
		ConfigOverride: map[string]any{
			"model": "gpt-4",
			"top_p": 0.9,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedCfg)
	assert.Equal(t, "gpt-4", capturedCfg["model"])   // overridden
	assert.Equal(t, 0.5, capturedCfg["temperature"]) // kept from default
	assert.Equal(t, 0.9, capturedCfg["top_p"])       // new from override
}

func TestEvalHandlerEvaluatorFiltering(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()

	evalA := experiment.NewEvaluator("eval-a", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
		return "a", nil
	})
	evalB := experiment.NewEvaluator("eval-b", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
		return "b", nil
	})
	evalC := experiment.NewEvaluator("eval-c", func(ctx context.Context, rec dataset.Record, output any) (any, error) {
		return "c", nil
	})

	defs := []*ExperimentDefinition{{
		Name:        "test-exp",
		ProjectName: "test-project",
		Task:        task,
		Dataset:     ds,
		Evaluators:  []experiment.Evaluator{evalA, evalB, evalC},
	}}
	registry := NewRegistry(defs)
	handler := NewEvalHandler(registry)

	body, _ := json.Marshal(EvalRequest{
		Name:       "test-exp",
		Stream:     false,
		Evaluators: []string{"eval-a", "eval-c"},
	})
	req := httptest.NewRequest(http.MethodPost, "/eval", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	results, ok := resp["results"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, results)

	// Each result should have exactly 2 evaluations (eval-a and eval-c)
	for _, r := range results {
		result := r.(map[string]any)
		evals, ok := result["Evaluations"].([]any)
		require.True(t, ok)
		assert.Len(t, evals, 2)
		evalNames := make([]string, 0, len(evals))
		for _, e := range evals {
			evalMap := e.(map[string]any)
			evalNames = append(evalNames, evalMap["Name"].(string))
		}
		assert.ElementsMatch(t, []string{"eval-a", "eval-c"}, evalNames)
	}
}

func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("sets-cors-headers", func(t *testing.T) {
		handler := corsMiddleware(inner, []string{"*"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "GET, POST, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
		assert.Equal(t, "Content-Type, Authorization", rec.Header().Get("Access-Control-Allow-Headers"))
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("preflight-request", func(t *testing.T) {
		handler := corsMiddleware(inner, []string{"*"})
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("custom-origins", func(t *testing.T) {
		handler := corsMiddleware(inner, []string{"http://localhost:3000", "http://localhost:8080"})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		assert.Equal(t, "http://localhost:3000, http://localhost:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestServerHandler(t *testing.T) {
	testTracer(t)

	ds := createTestDataset(t)
	task := createTestTask()
	evaluators := createTestEvaluators()

	srv := New(
		[]*ExperimentDefinition{{
			Name:        "test-exp",
			ProjectName: "test-project",
			Task:        task,
			Dataset:     ds,
			Evaluators:  evaluators,
		}},
		WithAddr(":0"),
		WithCORSOrigins("http://localhost:3000"),
	)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("list-via-server", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/list")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "http://localhost:3000", resp.Header.Get("Access-Control-Allow-Origin"))

		var body map[string][]listExperimentView
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		assert.Len(t, body["experiments"], 1)
	})

	t.Run("eval-via-server", func(t *testing.T) {
		reqBody, _ := json.Marshal(EvalRequest{Name: "test-exp", Stream: false})
		resp, err := http.Post(ts.URL+"/eval", "application/json", bytes.NewReader(reqBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestMergeConfig(t *testing.T) {
	t.Run("nil-inputs", func(t *testing.T) {
		assert.Nil(t, mergeConfig(nil, nil))
	})
	t.Run("defaults-only", func(t *testing.T) {
		result := mergeConfig(map[string]any{"a": 1}, nil)
		assert.Equal(t, map[string]any{"a": 1}, result)
	})
	t.Run("overrides-only", func(t *testing.T) {
		result := mergeConfig(nil, map[string]any{"a": 1})
		assert.Equal(t, map[string]any{"a": 1}, result)
	})
	t.Run("merge", func(t *testing.T) {
		result := mergeConfig(
			map[string]any{"a": 1, "b": 2},
			map[string]any{"b": 3, "c": 4},
		)
		assert.Equal(t, map[string]any{"a": 1, "b": 3, "c": 4}, result)
	})
}

func TestFilterEvaluators(t *testing.T) {
	evalA := experiment.NewEvaluator("a", nil)
	evalB := experiment.NewEvaluator("b", nil)
	evalC := experiment.NewEvaluator("c", nil)
	all := []experiment.Evaluator{evalA, evalB, evalC}

	t.Run("no-filter", func(t *testing.T) {
		result := filterEvaluators(all, nil)
		assert.Empty(t, result)
	})
	t.Run("filter-subset", func(t *testing.T) {
		result := filterEvaluators(all, []string{"a", "c"})
		require.Len(t, result, 2)
		assert.Equal(t, "a", result[0].Name())
		assert.Equal(t, "c", result[1].Name())
	})
	t.Run("filter-nonexistent", func(t *testing.T) {
		result := filterEvaluators(all, []string{"z"})
		assert.Empty(t, result)
	})
}

// --- Test helpers (mirrors experiment_test.go patterns) ---

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
				Name:           "test-exp",
				Description:    "A test experiment",
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
					Description:    "Test dataset",
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
	ds, err := dataset.Create(context.Background(), "test-dataset", records, dataset.WithDescription("Test dataset"))
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

func parseStreamEvents(t *testing.T, body *bytes.Buffer) []StreamEvent {
	t.Helper()
	var events []StreamEvent
	dec := json.NewDecoder(body)
	for dec.More() {
		var event StreamEvent
		if err := dec.Decode(&event); err != nil {
			break
		}
		events = append(events, event)
	}
	return events
}
