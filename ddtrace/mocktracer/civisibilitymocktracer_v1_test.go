// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package mocktracer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/require"
	v1mocktracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	v1tracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestCIVisibilityMockTracerSupportsV1Spans(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	t.Setenv(constants.APIKeyEnvironmentVariable, "dummy")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")

	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		tracer.Stop()
		civisibility.ResetForTesting()
	})

	mock := v1mocktracer.Start()
	t.Cleanup(mock.Stop)
	span, _ := v1tracer.StartSpanFromContext(context.Background(), "v1.application")
	require.NotPanics(t, func() { span.Finish() })
	require.Len(t, mock.FinishedSpans(), 1)
}
