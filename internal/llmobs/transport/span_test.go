// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func TestNewPushSpanEventsRequests(t *testing.T) {
	events := []*LLMObsSpanEvent{
		{SpanID: "span-1", DDAttributes: DDAttributes{Scope: "scope-1"}},
		{SpanID: "span-2"},
	}
	requests := NewPushSpanEventsRequests(events)
	require.Len(t, requests, 2)

	for i, request := range requests {
		assert.Equal(t, "raw", request.Stage)
		assert.Equal(t, version.Tag, request.TracerVersion)
		assert.Equal(t, "span", request.EventType)
		require.Len(t, request.Spans, 1)
		assert.Same(t, events[i], request.Spans[0])
	}
	assert.Equal(t, "scope-1", requests[0].Scope)
	assert.Empty(t, requests[1].Scope)
}

func TestPushSpanEventsInputValidation(t *testing.T) {
	transport := &Transport{}

	result, err := transport.PushSpanEventsWithResult(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, RequestResult{}, result)

	result, err = transport.PushSpanEventsWithResult(context.Background(), []*LLMObsSpanEvent{{
		Meta: map[string]any{"bad": unencodableValue{}},
	}})
	require.ErrorContains(t, err, "failed to json encode body")
	assert.Equal(t, RequestResult{}, result)

	result, err = transport.PushSpanEventsBodyWithResult(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, RequestResult{}, result)
}

func TestPushSpanEventsBodyWithResultForwardsPreparedBody(t *testing.T) {
	prepared := []byte(`{"prepared":true}`)
	response := []byte(`{"accepted":true}`)
	var received []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, endpointLLMSpan, request.URL.Path)
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		var err error
		received, err = io.ReadAll(request.Body)
		assert.NoError(t, err)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(response)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushSpanEventsBodyWithResult(context.Background(), prepared)
	require.NoError(t, err)
	assert.Equal(t, prepared, received)
	assert.Equal(t, RequestResult{
		StatusCode: http.StatusAccepted,
		Attempts:   1,
		Body:       response,
	}, result)
}

func TestPushSpanEventsBodyWithResultCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	transport := newTestTransport(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := transport.PushSpanEventsBodyWithResult(ctx, []byte(`{}`))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, result.Attempts)
	assert.False(t, result.Retriable)
}

func TestPushSpanEventsBodyWithResultRejectsUnexpectedSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	transport := newTestTransport(t, server.URL)
	result, err := transport.PushSpanEventsBodyWithResult(context.Background(), []byte(`{}`))
	require.ErrorContains(t, err, "unexpected status 204")
	assert.Equal(t, http.StatusNoContent, result.StatusCode)
	assert.Equal(t, 1, result.Attempts)
}
