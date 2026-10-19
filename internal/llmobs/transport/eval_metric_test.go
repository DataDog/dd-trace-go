// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPushMetricsRequest(t *testing.T) {
	metrics := []*LLMObsMetric{{Label: "quality"}}
	request := NewPushMetricsRequest(metrics)

	assert.Equal(t, "evaluation_metric", request.Data.Type)
	assert.Equal(t, metrics, request.Data.Attributes.Metrics)
}

func TestPushEvalMetricsWithResult(t *testing.T) {
	var received PushMetricsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, endpointEvalMetric, request.URL.Path)
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		assert.NoError(t, json.NewDecoder(request.Body).Decode(&received))
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	require.NoError(t, transport.PushEvalMetrics(context.Background(), nil))
	value := 0.9
	result, err := transport.PushEvalMetricsWithResult(context.Background(), []*LLMObsMetric{{
		Label:      "quality",
		MetricType: EvalMetricTypeScore,
		ScoreValue: &value,
	}})
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, result.StatusCode)
	assert.Equal(t, 1, result.Attempts)
	assert.Equal(t, []byte(`{"accepted":true}`), result.Body)
	assert.False(t, result.Retriable)
	assert.Equal(t, "evaluation_metric", received.Data.Type)
	require.Len(t, received.Data.Attributes.Metrics, 1)
	assert.Equal(t, "quality", received.Data.Attributes.Metrics[0].Label)
}

func TestPushEvalMetricsInputValidation(t *testing.T) {
	transport := &Transport{}

	result, err := transport.PushEvalMetricsWithResult(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, RequestResult{}, result)

	result, err = transport.PushEvalMetricsWithResult(context.Background(), []*LLMObsMetric{{
		JSONValue: map[string]any{"bad": unencodableValue{}},
	}})
	require.ErrorContains(t, err, "failed to json encode body")
	assert.Equal(t, RequestResult{}, result)

	result, err = transport.PushEvalMetricsBodyWithResult(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, RequestResult{}, result)
}

func TestPushEvalMetricsBodyWithResultForwardsPreparedBody(t *testing.T) {
	prepared := []byte(`{"prepared":true}`)
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var err error
		received, err = io.ReadAll(request.Body)
		assert.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushEvalMetricsBodyWithResult(context.Background(), prepared)
	require.NoError(t, err)
	assert.Equal(t, prepared, received)
	assert.Equal(t, http.StatusOK, result.StatusCode)
	assert.Equal(t, 1, result.Attempts)
}

func TestPushEvalMetricsBodyWithResultFailure(t *testing.T) {
	response := []byte(`{"error":"invalid"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(response)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushEvalMetricsBodyWithResult(context.Background(), []byte(`{}`))
	require.ErrorContains(t, err, "request failed with http status code: 400")
	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	assert.Equal(t, 1, result.Attempts)
	assert.Equal(t, response, result.Body)
	assert.False(t, result.Retriable)
}

func TestPushEvalMetricsBodyWithResultRejectsUnexpectedSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushEvalMetricsBodyWithResult(context.Background(), []byte(`{}`))
	require.ErrorContains(t, err, "unexpected status 204")
	assert.Equal(t, http.StatusNoContent, result.StatusCode)
	assert.Equal(t, 1, result.Attempts)
}

func TestPushEvalMetricsBodyWithResultCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	transport := newTestTransport(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := transport.PushEvalMetricsBodyWithResult(ctx, []byte(`{}`))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, result.Attempts)
	assert.False(t, result.Retriable)
}
