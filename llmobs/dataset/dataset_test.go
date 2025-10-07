// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package dataset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	llmobstransport "github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

const (
	testAPIKey = "abcd1234efgh5678ijkl9012mnop3456"
	testAppKey = "test-app-key"
)

func TestDatasetCreation(t *testing.T) {
	t.Run("successful-creation", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "What is the capital of France?"},
				ExpectedOutput: "Paris",
				Metadata:       map[string]any{"category": "geography"},
			},
			{
				Input:          map[string]any{"question": "What is the capital of Germany?"},
				ExpectedOutput: "Berlin",
				Metadata:       map[string]any{"category": "geography"},
			},
		}

		ds, err := Create(
			context.Background(),
			"test-dataset",
			records,
			WithDescription("Test dataset for integration tests"),
		)

		require.NoError(t, err)
		require.NotNil(t, ds)
		assert.Equal(t, "test-dataset", ds.Name())
		assert.Equal(t, "test-dataset-id", ds.ID())
		assert.Equal(t, 2, ds.Version())
		assert.Equal(t, 2, ds.Len())
	})
	t.Run("creation-with-options", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"text": "Hello world"},
				ExpectedOutput: "greeting",
			},
		}

		ds, err := Create(
			context.Background(),
			"test-dataset-with-options",
			records,
			WithDescription("Dataset with custom description"),
		)

		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "test-dataset-with-options", ds.Name())
		assert.Equal(t, 1, ds.Len())
	})
	t.Run("with-project-name-option", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"text": "Hello world"},
				ExpectedOutput: "greeting",
			},
		}

		ds, err := Create(
			context.Background(),
			"test-dataset-with-project",
			records,
			WithDescription("Dataset with custom project name"),
			WithProjectName("custom-project"),
		)

		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "test-dataset-with-project", ds.Name())
		assert.Equal(t, 1, ds.Len())
	})
	t.Run("missing-dd-app-key-agentless", func(t *testing.T) {
		t.Setenv("DD_API_KEY", testAPIKey)
		t.Setenv("DD_APP_KEY", "")

		// Use agentless mode to trigger app key requirement
		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsAgentlessEnabled(true)))
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "test"},
				ExpectedOutput: "answer",
			},
		}

		_, err := Create(
			context.Background(),
			"test-dataset",
			records,
		)

		// Should fail - datasets require DD_APP_KEY in agentless mode
		require.Error(t, err)
		assert.Contains(t, err.Error(), "an app key must be provided")
	})
	t.Run("missing-dd-app-key-agent-mode", func(t *testing.T) {
		t.Setenv("DD_APP_KEY", "")

		// Use agent mode - app key should not be required
		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsAgentlessEnabled(false)))
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "test"},
				ExpectedOutput: "answer",
			},
		}

		ds, err := Create(
			context.Background(),
			"test-dataset",
			records,
		)

		// Should succeed - app key not required in agent mode
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "test-dataset", ds.Name())
	})
	t.Run("missing-project-name", func(t *testing.T) {

		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsProjectName("")))
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "test"},
				ExpectedOutput: "answer",
			},
		}

		_, err := Create(context.Background(), "test-dataset", records)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project name must be provided")
	})
	t.Run("empty-records", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		ds, err := Create(
			context.Background(),
			"test-dataset",
			[]Record{},
		)

		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, 0, ds.Len())
	})
}

func TestDatasetCRUDOperations(t *testing.T) {
	t.Run("append-records", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create initial dataset
		initialRecords := []Record{
			{
				Input:          map[string]any{"question": "What is 2+2?"},
				ExpectedOutput: "4",
			},
		}

		ds, err := Create(context.Background(), "test-dataset", initialRecords)
		require.NoError(t, err)
		assert.Equal(t, 1, ds.Len())

		// Append new records
		ds.Append(Record{
			Input:          map[string]any{"question": "What is 3+3?"},
			ExpectedOutput: "6",
		})
		ds.Append(Record{
			Input:          map[string]any{"question": "What is 4+4?"},
			ExpectedOutput: "8",
		})

		// Push changes
		err = ds.Push(context.Background())
		require.NoError(t, err)

		// Verify length increased
		assert.Equal(t, 3, ds.Len())
	})
	t.Run("update-records", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create dataset with records
		records := []Record{
			{
				Input:          map[string]any{"question": "Original question"},
				ExpectedOutput: "Original answer",
				Metadata:       map[string]any{"version": "v1"},
			},
		}

		ds, err := Create(context.Background(), "test-dataset", records)
		require.NoError(t, err)

		// Update the record at index 0
		ds.Update(0, RecordUpdate{
			Input:          map[string]any{"question": "Updated question"},
			ExpectedOutput: "Updated answer",
			Metadata:       map[string]any{"version": "v2"},
		})

		// Push changes
		err = ds.Push(context.Background())
		require.NoError(t, err)

		// Verify record was updated (we can't directly verify the content
		// since the mock doesn't return updated data, but we can verify no errors)
		assert.Equal(t, 1, ds.Len())
	})
	t.Run("delete-records", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create dataset with multiple records
		records := []Record{
			{
				Input:          map[string]any{"question": "Keep this"},
				ExpectedOutput: "answer1",
			},
			{
				Input:          map[string]any{"question": "Delete this"},
				ExpectedOutput: "answer2",
			},
		}

		ds, err := Create(context.Background(), "test-dataset", records)
		require.NoError(t, err)
		assert.Equal(t, 2, ds.Len())

		// Delete the record at index 1
		ds.Delete(1)

		// Push changes
		err = ds.Push(context.Background())
		require.NoError(t, err)

		// After deleting 1 record from 2, we should have 1 record left
		assert.Equal(t, 1, ds.Len())
	})
	t.Run("push-without-id-fails", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create a dataset manually without going through Create()
		ds := &Dataset{}
		ds.Append(Record{
			Input:          map[string]any{"test": "data"},
			ExpectedOutput: "result",
		})

		// Push should fail because dataset has no ID
		err := ds.Push(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "dataset has no ID")
	})
	t.Run("bulk-upload-for-large-datasets", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create initial dataset with one record
		initialRecords := []Record{
			{
				Input:          map[string]any{"small": "data"},
				ExpectedOutput: "small",
			},
		}

		ds, err := Create(context.Background(), "test-dataset", initialRecords)
		require.NoError(t, err)

		// Create a large string that will push us over the 5MB threshold
		// We need > 5MB of data. Each record with this data will be ~1.5MB when JSON-encoded
		largeString := strings.Repeat("x", 1500000) // ~1.5MB

		// Append 4 large records to exceed the 5MB threshold
		for i := 0; i < 4; i++ {
			ds.Append(Record{
				Input: map[string]any{
					"large_field": largeString,
					"index":       i,
				},
				ExpectedOutput: fmt.Sprintf("output-%d", i),
				Metadata:       map[string]any{"size": "large"},
			})
		}

		// Push should use bulk upload because the delta exceeds 5MB
		err = ds.Push(context.Background())
		require.NoError(t, err)

		// Verify all records are present
		assert.Equal(t, 5, ds.Len()) // 1 initial + 4 appended
	})
}

func TestDatasetCSVImport(t *testing.T) {
	t.Run("successful-csv-import", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create a temporary CSV file
		csvContent := `question,answer,category
What is the capital of France?,Paris,geography
What is 2+2?,4,math
What is the largest planet?,Jupiter,astronomy`

		csvFile := createTempCSV(t, csvContent)
		defer os.Remove(csvFile)

		// Import from CSV
		ds, err := CreateFromCSV(
			context.Background(),
			"csv-dataset",
			csvFile,
			[]string{"question"}, // input columns
			WithDescription("Dataset imported from CSV"),
			WithCSVExpectedOutputColumns([]string{"answer"}),
			WithCSVMetadataColumns([]string{"category"}),
		)

		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "csv-dataset", ds.Name())
		assert.Equal(t, 3, ds.Len())

		// Verify records were imported correctly
		rec, ok := ds.Record(0)
		require.True(t, ok)

		assert.Equal(t, map[string]any{"question": "What is the capital of France?"}, rec.Input)
		assert.Equal(t, map[string]any{"answer": "Paris"}, rec.ExpectedOutput)
		assert.Equal(t, map[string]any{"category": "geography"}, rec.Metadata)
	})
	t.Run("csv-with-project-name-option", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		csvContent := "question,answer,category\nWhat is 2+2?,4,math\nWhat is the capital of Spain?,Madrid,geography\n"
		csvFile := createTempCSV(t, csvContent)
		defer os.Remove(csvFile)

		ds, err := CreateFromCSV(
			context.Background(),
			"csv-dataset-with-project",
			csvFile,
			[]string{"question"},
			WithCSVExpectedOutputColumns([]string{"answer"}),
			WithCSVMetadataColumns([]string{"category"}),
			WithProjectName("csv-custom-project"),
		)

		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "csv-dataset-with-project", ds.Name())
		assert.Equal(t, 2, ds.Len())
	})
	t.Run("csv-with-custom-delimiter", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// Create CSV with semicolon delimiter
		csvContent := `question;answer;category
What is the capital of Spain?;Madrid;geography
What is 5+5?;10;math`

		csvFile := createTempCSV(t, csvContent)
		defer os.Remove(csvFile)

		ds, err := CreateFromCSV(
			context.Background(),
			"test-dataset",
			csvFile,
			[]string{"question"},
			WithCSVDelimiter(';'),
			WithCSVExpectedOutputColumns([]string{"answer"}),
			WithCSVMetadataColumns([]string{"category"}),
		)

		require.NoError(t, err)
		assert.Equal(t, 2, ds.Len())
	})
	t.Run("csv-missing-columns", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		csvContent := `question,answer
What is the capital of Italy?,Rome`

		csvFile := createTempCSV(t, csvContent)
		defer os.Remove(csvFile)

		// Try to import with missing metadata column
		_, err := CreateFromCSV(
			context.Background(),
			"test-dataset",
			csvFile,
			[]string{"question"},
			WithCSVMetadataColumns([]string{"category"}), // This column doesn't exist
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata columns not found in CSV header")
	})
	t.Run("csv-empty-file", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		csvFile := createTempCSV(t, "")
		defer os.Remove(csvFile)

		_, err := CreateFromCSV(
			context.Background(),
			"test-dataset",
			csvFile,
			[]string{"question"},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "CSV file appears to be empty")
	})
	t.Run("csv-nonexistent-file", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		_, err := CreateFromCSV(
			context.Background(),
			"test-dataset",
			"/nonexistent/file.csv",
			[]string{"question"},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to open csv")
	})
	t.Run("csv-missing-project-name", func(t *testing.T) {

		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsProjectName("")))
		defer tt.Stop()

		csvFile := createTempCSV(t, "question,answer\nWhat is 2+2?,4\n")
		defer os.Remove(csvFile)

		_, err := CreateFromCSV(
			context.Background(),
			"test-dataset",
			csvFile,
			[]string{"question"},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "project name must be provided")
	})
}

func TestDatasetPull(t *testing.T) {
	t.Run("successful-pull", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		ds, err := Pull(context.Background(), "existing-dataset")
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "existing-dataset", ds.Name())
		assert.Equal(t, "existing-dataset-id", ds.ID())
		// Mock returns 2 records
		assert.Equal(t, 2, ds.Len())
	})
	t.Run("pull-with-project-name-option", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		ds, err := Pull(
			context.Background(),
			"existing-dataset",
			WithPullProjectName("custom-pull-project"),
		)
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "existing-dataset", ds.Name())
		assert.Equal(t, "existing-dataset-id", ds.ID())
		assert.Equal(t, 2, ds.Len())
	})
	t.Run("pull-nonexistent-dataset", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		// The mock handler will return an error for unknown datasets
		_, err := Pull(context.Background(), "nonexistent-dataset")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get dataset")
	})
	t.Run("pull-missing-project-name", func(t *testing.T) {

		tt := testTracer(t, testtracer.WithTracerStartOpts(tracer.WithLLMObsProjectName("")))
		defer tt.Stop()

		_, err := Pull(context.Background(), "existing-dataset")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project name must be provided")
	})
	t.Run("pull-dataset-with-non-map-input", func(t *testing.T) {
		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
			if !strings.HasPrefix(path, "/api/unstable/llm-obs/v1") {
				return nil
			}
			path = strings.TrimPrefix(path, "/api/unstable/llm-obs/v1")

			switch {
			case path == "/projects" && r.Method == http.MethodPost:
				return handleMockProjectCreate(r)
			case strings.Contains(path, "/datasets") && r.Method == http.MethodGet && !strings.Contains(path, "/records"):
				response := llmobstransport.GetDatasetResponse{
					Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
						{
							ID:   "string-input-dataset-id",
							Type: "datasets",
							Attributes: llmobstransport.DatasetView{
								ID:             "string-input-dataset-id",
								Name:           "string-input-dataset",
								Description:    "Dataset with string input",
								CurrentVersion: 1,
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
			case strings.Contains(path, "/datasets/") && strings.HasSuffix(path, "/records") && r.Method == http.MethodGet:
				// Return records with string input (not a map) - this simulates a dataset created externally
				rawJSON := `{
					"data": [
						{
							"id": "record-1",
							"type": "dataset_records",
							"attributes": {
								"id": "record-1",
								"input": "This is a simple string input, not a map",
								"expected_output": "Some output",
								"version": 1
							}
						}
					]
				}`
				return &http.Response{
					Status:     "200 OK",
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(rawJSON)),
					Request:    r,
				}
			default:
				return nil
			}
		}

		tt := testTracer(t, testtracer.WithMockResponses(h))
		defer tt.Stop()

		ds, err := Pull(context.Background(), "string-input-dataset")
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "string-input-dataset", ds.Name())
		assert.Equal(t, 1, ds.Len())

		// Verify the record has the string input
		rec, ok := ds.Record(0)
		require.True(t, ok)
		assert.Equal(t, "This is a simple string input, not a map", rec.Input)
		assert.Equal(t, "Some output", rec.ExpectedOutput)
	})
	t.Run("pull-dataset-with-non-map-metadata", func(t *testing.T) {
		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
			if !strings.HasPrefix(path, "/api/unstable/llm-obs/v1") {
				return nil
			}
			path = strings.TrimPrefix(path, "/api/unstable/llm-obs/v1")

			switch {
			case path == "/projects" && r.Method == http.MethodPost:
				return handleMockProjectCreate(r)
			case strings.Contains(path, "/datasets") && r.Method == http.MethodGet && !strings.Contains(path, "/records"):
				response := llmobstransport.GetDatasetResponse{
					Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
						{
							ID:   "string-metadata-dataset-id",
							Type: "datasets",
							Attributes: llmobstransport.DatasetView{
								ID:             "string-metadata-dataset-id",
								Name:           "string-metadata-dataset",
								Description:    "Dataset with string metadata",
								CurrentVersion: 1,
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
			case strings.Contains(path, "/datasets/") && strings.HasSuffix(path, "/records") && r.Method == http.MethodGet:
				// Return records with string metadata (not a map) - this simulates a dataset created externally
				rawJSON := `{
					"data": [
						{
							"id": "record-1",
							"type": "dataset_records",
							"attributes": {
								"id": "record-1",
								"input": {"question": "What is AI?"},
								"expected_output": "Artificial Intelligence",
								"metadata": "simple string metadata from UI",
								"version": 1
							}
						}
					]
				}`
				return &http.Response{
					Status:     "200 OK",
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(rawJSON)),
					Request:    r,
				}
			default:
				return nil
			}
		}

		tt := testTracer(t, testtracer.WithMockResponses(h))
		defer tt.Stop()

		// This should succeed - metadata can be any type, not just map[string]any
		ds, err := Pull(context.Background(), "string-metadata-dataset")
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "string-metadata-dataset", ds.Name())
		assert.Equal(t, 1, ds.Len())

		// Verify the record has the string metadata
		rec, ok := ds.Record(0)
		require.True(t, ok)
		inputMap, ok := rec.Input.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "What is AI?", inputMap["question"])
		assert.Equal(t, "Artificial Intelligence", rec.ExpectedOutput)
		assert.Equal(t, "simple string metadata from UI", rec.Metadata)
	})
	t.Run("pull-dataset-with-pagination", func(t *testing.T) {
		// Track which page we're on to simulate pagination
		var requestCount int

		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
			if !strings.HasPrefix(path, "/api/unstable/llm-obs/v1") {
				return nil
			}
			path = strings.TrimPrefix(path, "/api/unstable/llm-obs/v1")

			switch {
			case path == "/projects" && r.Method == http.MethodPost:
				return handleMockProjectCreate(r)
			case strings.Contains(path, "/datasets") && r.Method == http.MethodGet && !strings.Contains(path, "/records"):
				response := llmobstransport.GetDatasetResponse{
					Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
						{
							ID:   "paginated-dataset-id",
							Type: "datasets",
							Attributes: llmobstransport.DatasetView{
								ID:             "paginated-dataset-id",
								Name:           "paginated-dataset",
								Description:    "Dataset with pagination",
								CurrentVersion: 1,
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
			case strings.Contains(path, "/datasets/") && strings.Contains(path, "/records") && r.Method == http.MethodGet:
				// Simulate 3 pages of results
				requestCount++
				var rawJSON string

				switch requestCount {
				case 1:
					// First page - has cursor for next page
					rawJSON = `{
						"data": [
							{
								"id": "record-1",
								"type": "dataset_records",
								"attributes": {
									"id": "record-1",
									"input": {"question": "Q1"},
									"expected_output": "A1",
									"metadata": {},
									"version": 1
								}
							},
							{
								"id": "record-2",
								"type": "dataset_records",
								"attributes": {
									"id": "record-2",
									"input": {"question": "Q2"},
									"expected_output": "A2",
									"metadata": {},
									"version": 1
								}
							}
						],
						"meta": {
							"after": "cursor-page-2"
						}
					}`
				case 2:
					// Second page - has cursor for next page
					rawJSON = `{
						"data": [
							{
								"id": "record-3",
								"type": "dataset_records",
								"attributes": {
									"id": "record-3",
									"input": {"question": "Q3"},
									"expected_output": "A3",
									"metadata": {},
									"version": 1
								}
							}
						],
						"meta": {
							"after": "cursor-page-3"
						}
					}`
				default:
					// Third page - no cursor (last page)
					rawJSON = `{
						"data": [
							{
								"id": "record-4",
								"type": "dataset_records",
								"attributes": {
									"id": "record-4",
									"input": {"question": "Q4"},
									"expected_output": "A4",
									"metadata": {},
									"version": 1
								}
							}
						],
						"meta": {}
					}`
				}

				return &http.Response{
					Status:     "200 OK",
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(rawJSON)),
					Request:    r,
				}
			default:
				return nil
			}
		}

		tt := testTracer(t, testtracer.WithMockResponses(h))
		defer tt.Stop()

		// Pull the dataset - should fetch all pages automatically (eager loading)
		ds, err := Pull(context.Background(), "paginated-dataset")
		require.NoError(t, err)
		assert.NotNil(t, ds)
		assert.Equal(t, "paginated-dataset", ds.Name())

		// Verify all records from all pages were fetched
		assert.Equal(t, 4, ds.Len(), "should have fetched all records across all pages")
		assert.Equal(t, 3, requestCount, "should have made 3 requests for 3 pages")

		// Verify each record
		for i := 0; i < 4; i++ {
			rec, ok := ds.Record(i)
			require.True(t, ok, "record %d should exist", i)
			inputMap, ok := rec.Input.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, fmt.Sprintf("Q%d", i+1), inputMap["question"])
			assert.Equal(t, fmt.Sprintf("A%d", i+1), rec.ExpectedOutput)
		}
	})
}

func TestDatasetRecordIteration(t *testing.T) {
	t.Run("records-iterator", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "Q1"},
				ExpectedOutput: "A1",
			},
			{
				Input:          map[string]any{"question": "Q2"},
				ExpectedOutput: "A2",
			},
			{
				Input:          map[string]any{"question": "Q3"},
				ExpectedOutput: "A3",
			},
		}

		ds, err := Create(context.Background(), "test-dataset", records)
		require.NoError(t, err)

		// Test iterator
		count := 0
		for i, rec := range ds.Records() {
			assert.Equal(t, count, i)
			inputMap, ok := rec.Input.(map[string]any)
			require.True(t, ok, "Input should be a map")
			assert.NotEmpty(t, inputMap["question"])
			count++
		}
		assert.Equal(t, 3, count)
	})
	t.Run("record-by-index", func(t *testing.T) {
		tt := testTracer(t)
		defer tt.Stop()

		records := []Record{
			{
				Input:          map[string]any{"question": "Test question"},
				ExpectedOutput: "Test answer",
			},
		}

		ds, err := Create(context.Background(), "test-dataset", records)
		require.NoError(t, err)

		// Test valid index
		rec, ok := ds.Record(0)
		require.True(t, ok)
		inputMap, ok := rec.Input.(map[string]any)
		require.True(t, ok, "Input should be a map")
		assert.Equal(t, "Test question", inputMap["question"])

		// Test invalid indices
		_, ok = ds.Record(-1)
		assert.False(t, ok)

		_, ok = ds.Record(1)
		assert.False(t, ok)
	})
}

func TestDatasetURL(t *testing.T) {
	run := func(t *testing.T) string {
		tt := testTracer(t)
		defer tt.Stop()

		ds, err := Create(context.Background(), "test-dataset", []Record{})
		require.NoError(t, err)

		return ds.URL()
	}
	t.Run("with-dd-site", func(t *testing.T) {
		t.Setenv("DD_SITE", "my-dd-site")
		url := run(t)
		assert.Equal(t, url, "https://my-dd-site/llm/datasets/test-dataset-id")
	})
	t.Run("empty-dd-site", func(t *testing.T) {
		url := run(t)
		assert.Equal(t, "https://app.datadoghq.com/llm/datasets/test-dataset-id", url)
	})
}

func TestDDAppKeyHeader(t *testing.T) {
	t.Run("dd-app-key-header-agentless", func(t *testing.T) {
		t.Setenv("DD_API_KEY", testAPIKey)
		t.Setenv("DD_APP_KEY", testAppKey)

		var capturedHeaders http.Header
		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
			if strings.Contains(path, "/api/unstable/llm-obs/v1/datasets") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default dataset mock handler handle the response
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

		_, err := Create(
			context.Background(),
			"test-dataset",
			[]Record{
				{
					Input:          map[string]any{"question": "test"},
					ExpectedOutput: "answer",
				},
			},
			WithDescription("Test DD_APP_KEY header in agentless mode"),
		)
		require.NoError(t, err)

		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, testAppKey, capturedHeaders.Get("DD-APPLICATION-KEY"), "DD-APPLICATION-KEY header should be set in agentless mode")
	})
	t.Run("dd-app-key-header-agent-mode", func(t *testing.T) {
		var capturedHeaders http.Header
		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")

			if strings.Contains(path, "/api/unstable/llm-obs/v1/datasets") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default dataset mock handler handle the response
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

		_, err := Create(
			context.Background(),
			"test-dataset",
			[]Record{
				{
					Input:          map[string]any{"question": "test"},
					ExpectedOutput: "answer",
				},
			},
			WithDescription("Test DD_APP_KEY header in agent mode"),
		)
		require.NoError(t, err)

		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, "true", capturedHeaders.Get("X-Datadog-NeedsAppKey"), "X-Datadog-NeedsAppKey header should always be set in agent mode")
		assert.Empty(t, capturedHeaders.Get("DD-APPLICATION-KEY"), "DD-APPLICATION-KEY header should not be set in agent mode")
	})
	t.Run("needs-app-key-header-agent-mode", func(t *testing.T) {
		t.Setenv("DD_APP_KEY", "") // No app key provided

		var capturedHeaders http.Header
		h := func(r *http.Request) *http.Response {
			path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")

			if strings.Contains(path, "/api/unstable/llm-obs/v1/datasets") {
				capturedHeaders = r.Header.Clone()
			}
			// Let the default dataset mock handler handle the response
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

		_, err := Create(context.Background(), "test-dataset", []Record{
			{
				Input:          map[string]any{"question": "test"},
				ExpectedOutput: "answer",
			},
		})
		require.NoError(t, err)

		require.NotNil(t, capturedHeaders, "No headers were captured")
		assert.Equal(t, "true", capturedHeaders.Get("X-Datadog-NeedsAppKey"), "X-Datadog-NeedsAppKey header should be set when no app key is provided in agent mode")
		assert.Empty(t, capturedHeaders.Get("DD-APPLICATION-KEY"), "DD-APPLICATION-KEY header should not be set when no app key is provided")
	})
}

func BenchmarkDatasetIterator(b *testing.B) {
	b.ReportAllocs()

	records := generateRandomRecords(10000)
	ds := &Dataset{records: records}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for range ds.Records() {
		}
	}
}

func BenchmarkDatasetLoop(b *testing.B) {
	b.ReportAllocs()

	records := generateRandomRecords(10000)
	ds := &Dataset{records: records}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		for range ds.records {
		}
	}
	b.StopTimer()
}

// Helper functions

func randomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func randomMap(k int) map[string]any {
	m := make(map[string]any, k)
	for i := 0; i < k; i++ {
		key := randomString(5)
		// Randomly assign int or string
		if rand.Intn(2) == 0 {
			m[key] = rand.Intn(1000)
		} else {
			m[key] = randomString(8)
		}
	}
	return m
}

func randomRecord() *Record {
	return &Record{
		id:             randomString(10),
		Input:          randomMap(rand.Intn(5) + 1), // 1–5 entries
		ExpectedOutput: rand.Intn(1000),             // simple int for example
		Metadata:       randomMap(rand.Intn(3) + 1), // 1–3 entries
		version:        rand.Intn(10),               // version 0–9
	}
}

func generateRandomRecords(n int) []*Record {
	records := make([]*Record, n)
	for i := 0; i < n; i++ {
		records[i] = randomRecord()
	}
	return records
}

func testTracer(t *testing.T, opts ...testtracer.Option) *testtracer.TestTracer {
	tracerOpts := []tracer.StartOption{
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("test-app"),
		tracer.WithLLMObsProjectName("test-project"),
		tracer.WithLLMObsAgentlessEnabled(false),
		tracer.WithService("test-service"),
		tracer.WithLogStartup(false),
	}
	defaultOpts := []testtracer.Option{
		testtracer.WithTracerStartOpts(tracerOpts...),
		testtracer.WithMockResponses(createMockHandler()),
	}
	allOpts := append(defaultOpts, opts...)
	tt := testtracer.Start(t, allOpts...)
	t.Cleanup(tt.Stop)
	return tt
}

// createMockHandler creates a mock handler for dataset-related requests
func createMockHandler() testtracer.MockResponseFunc {
	state := newMockDatasetState()

	return func(r *http.Request) *http.Response {
		path := strings.TrimPrefix(r.URL.Path, "/evp_proxy/v2")
		if !strings.HasPrefix(path, "/api/unstable/llm-obs/v1") {
			return nil
		}
		path = strings.TrimPrefix(path, "/api/unstable/llm-obs/v1")

		switch {
		case path == "/projects" && r.Method == http.MethodPost:
			return handleMockProjectCreate(r)

		case strings.HasPrefix(path, "/datasets") && strings.HasSuffix(path, "/batch_update"):
			return handleMockDatasetBatchUpdate(r)

		case strings.Contains(path, "/datasets/") && strings.HasSuffix(path, "/records/upload") && r.Method == http.MethodPost:
			return handleMockDatasetBulkUpload(r)

		case strings.Contains(path, "/datasets") && r.Method == http.MethodGet && !strings.Contains(path, "/records"):
			return handleMockDatasetGet(r, state)

		case strings.Contains(path, "/datasets/") && strings.HasSuffix(path, "/records") && r.Method == http.MethodGet:
			return handleMockDatasetGet(r, state)

		case strings.Contains(path, "/datasets") && r.Method == http.MethodPost:
			return handleMockDatasetCreate(r, state)

		default:
			return nil
		}
	}
}

func handleMockProjectCreate(r *http.Request) *http.Response {
	// Mock project creation - always return the same project
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

func handleMockDatasetCreate(r *http.Request, state *mockDatasetState) *http.Response {
	// Parse the request body to get the dataset name
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body)) // Reset body for potential re-reading

	// The actual request structure matches CreateDatasetRequest
	var createReq llmobstransport.CreateDatasetRequest
	if err := json.Unmarshal(body, &createReq); err != nil {
		return &http.Response{
			Status:     "400 Bad Request",
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error": "invalid request body"}`)),
			Request:    r,
		}
	}

	name := createReq.Data.Attributes.Name
	description := createReq.Data.Attributes.Description

	// Check if dataset already exists
	if _, exists := state.getDataset(name); exists {
		return &http.Response{
			Status:     "400 Bad Request",
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error": "dataset already exists"}`)),
			Request:    r,
		}
	}

	// Create the dataset
	dataset := &llmobstransport.DatasetView{
		ID:             "test-dataset-id",
		Name:           name,
		Description:    description,
		CurrentVersion: 1,
	}
	state.addDataset(name, dataset)

	response := llmobstransport.CreateDatasetResponse{
		Data: llmobstransport.ResponseData[llmobstransport.DatasetView]{
			ID:         "test-dataset-id",
			Type:       "datasets",
			Attributes: *dataset,
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

func handleMockDatasetGet(r *http.Request, state *mockDatasetState) *http.Response {
	// Parse query parameters to determine if this is a specific dataset request
	name := r.URL.Query().Get("filter[name]")

	if name != "" {
		// Check if dataset exists in our state
		dataset, exists := state.getDataset(name)

		var response llmobstransport.GetDatasetResponse
		if exists && name != "nonexistent-dataset" {
			// Return the dataset if it exists
			response = llmobstransport.GetDatasetResponse{
				Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
					{
						ID:         dataset.ID,
						Type:       "datasets",
						Attributes: *dataset,
					},
				},
			}
		} else if name == "existing-dataset" {
			// Special case for pull tests - simulate an existing dataset
			response = llmobstransport.GetDatasetResponse{
				Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
					{
						ID:   "existing-dataset-id",
						Type: "datasets",
						Attributes: llmobstransport.DatasetView{
							ID:             "existing-dataset-id",
							Name:           "existing-dataset",
							Description:    "Existing dataset for pull tests",
							CurrentVersion: 1,
						},
					},
				},
			}
		} else {
			// Return empty list if dataset doesn't exist (not a 404)
			response = llmobstransport.GetDatasetResponse{
				Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{},
			}
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

	// Check if this is a request for dataset records
	if strings.Contains(r.URL.Path, "/records") {
		recordsResponse := llmobstransport.GetDatasetRecordsResponse{
			Data: []llmobstransport.ResponseData[llmobstransport.DatasetRecordView]{
				{
					ID:   "record-1",
					Type: "dataset_records",
					Attributes: llmobstransport.DatasetRecordView{
						ID:             "record-1",
						Input:          map[string]any{"question": "What is AI?"},
						ExpectedOutput: "Artificial Intelligence",
						Version:        1,
					},
				},
				{
					ID:   "record-2",
					Type: "dataset_records",
					Attributes: llmobstransport.DatasetRecordView{
						ID:             "record-2",
						Input:          map[string]any{"question": "What is ML?"},
						ExpectedOutput: "Machine Learning",
						Version:        1,
					},
				},
			},
		}
		respData, _ := json.Marshal(recordsResponse)
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(respData)),
			Request:    r,
		}
	}

	// Return mock dataset
	datasetResponse := llmobstransport.GetDatasetResponse{
		Data: []llmobstransport.ResponseData[llmobstransport.DatasetView]{
			{
				ID:   "existing-dataset-id",
				Type: "datasets",
				Attributes: llmobstransport.DatasetView{
					ID:             "existing-dataset-id",
					Name:           "existing-dataset",
					Description:    "Existing dataset from backend",
					CurrentVersion: 1,
				},
			},
		},
	}

	respData, _ := json.Marshal(datasetResponse)
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(respData)),
		Request:    r,
	}
}

func handleMockDatasetBatchUpdate(r *http.Request) *http.Response {
	// Parse the request body to understand what operations are being performed
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body)) // Reset body for potential re-reading

	var batchReq llmobstransport.BatchUpdateDatasetRequest
	if err := json.Unmarshal(body, &batchReq); err != nil {
		return &http.Response{
			Status:     "400 Bad Request",
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error": "invalid request body"}`)),
			Request:    r,
		}
	}

	attrs := batchReq.Data.Attributes

	// Create response data for updated records first, then inserted records
	var response llmobstransport.BatchUpdateDatasetResponse

	// Add updated records (these keep their existing IDs)
	for _, updateRec := range attrs.UpdateRecords {
		response.Data = append(response.Data, llmobstransport.ResponseData[llmobstransport.DatasetRecordView]{
			ID:   updateRec.ID, // Keep the existing ID for updates
			Type: "dataset_records",
			Attributes: llmobstransport.DatasetRecordView{
				ID:             updateRec.ID,
				Input:          updateRec.Input,
				ExpectedOutput: *updateRec.ExpectedOutput,
				Version:        2,
			},
		})
	}

	// Add inserted records (these get new IDs)
	for i, insertRec := range attrs.InsertRecords {
		newID := fmt.Sprintf("new-record-id-%d", i+1)
		response.Data = append(response.Data, llmobstransport.ResponseData[llmobstransport.DatasetRecordView]{
			ID:   newID,
			Type: "dataset_records",
			Attributes: llmobstransport.DatasetRecordView{
				ID:             newID,
				Input:          insertRec.Input,
				ExpectedOutput: insertRec.ExpectedOutput,
				Version:        2,
			},
		})
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

func handleMockDatasetBulkUpload(r *http.Request) *http.Response {
	// For bulk upload, we just need to verify the multipart form is valid
	// and return a success response. The actual CSV parsing is done on the backend.
	contentType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return &http.Response{
			Status:     "400 Bad Request",
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error": "expected multipart/form-data"}`)),
			Request:    r,
		}
	}

	// Read and verify the body exists (we don't parse CSV in tests)
	body, _ := io.ReadAll(r.Body)
	if len(body) == 0 {
		return &http.Response{
			Status:     "400 Bad Request",
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error": "empty body"}`)),
			Request:    r,
		}
	}

	// Return success response
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"status": "success"}`)),
		Request:    r,
	}
}

func createTempCSV(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	csvFile := filepath.Join(tmpDir, "test.csv")
	err := os.WriteFile(csvFile, []byte(content), 0644)
	require.NoError(t, err)
	return csvFile
}

type mockDatasetState struct {
	datasets map[string]*llmobstransport.DatasetView
	mu       sync.RWMutex
}

func newMockDatasetState() *mockDatasetState {
	return &mockDatasetState{
		datasets: make(map[string]*llmobstransport.DatasetView),
	}
}

func (s *mockDatasetState) addDataset(name string, dataset *llmobstransport.DatasetView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.datasets[name] = dataset
}

func (s *mockDatasetState) getDataset(name string) (*llmobstransport.DatasetView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dataset, exists := s.datasets[name]
	return dataset, exists
}
