// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awslambda

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

const invocations = 2

// TestLambda builds mainapp with Orchestrion once, then runs it once per
// lambda.Start* calling convention against a fake Lambda Runtime API. The
// Datadog Lambda Extension isn't running, so the tracer writes the finished
// spans to stdout, where the test reads them.
func TestLambda(t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("requires an orchestrion-built test binary; run via orchestrion go test")
	}
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	for _, variant := range []string{
		"Start",
		"StartWithOptions",
		"StartWithOptionsSpread",
		"StartHandlerFunc",
		"StartHandlerFuncSpread",
		"StartStruct",
		"StartNewHandler",
		"StartWithContext",
		"StartHandler",
		"StartHandlerWithContext",
		"Prewrapped",
		"Aliased",
		"AliasedStartHandlerFunc",
		"ExplicitHandlerFunc",
		"FunctionValue",
		"PrewrappedNewHandler",
		"PrewrappedInterface",
	} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			api, stdout, stderr := runLambda(t, bin, variant, `{"name":"test"}`, srv.URL)
			require.Emptyf(t, api.Errors(), "stderr:\n%s", stderr)

			requireTraces(t, parseSpans(t, stdout), stdout, !strings.HasPrefix(variant, "Prewrapped"))
		})
	}
}

// requireTraces checks that every invocation produced exactly one aws.lambda
// span, with application HTTP calls and, for runtime wrappers, the response POST
// as children. All children must finish before the invocation is flushed.
func requireTraces(t *testing.T, spans []span, stdout []byte, runtimeWrapped bool) {
	t.Helper()
	var lambdaSpans []span
	for _, s := range spans {
		if s.Name == "aws.lambda" {
			lambdaSpans = append(lambdaSpans, s)
		}
	}
	require.Lenf(t, lambdaSpans, invocations, "stdout:\n%s", stdout)

	for i, root := range lambdaSpans {
		assert.Equal(t, requestID(i), root.Meta["request_id"])
		assert.Equal(t, functionARN, root.Meta["function_arn"])
		assert.Equal(t, strconv.FormatBool(i == 0), root.Meta["cold_start"])

		var paths []string
		for _, s := range spans {
			if s.ParentID != root.SpanID {
				continue
			}
			assert.Equal(t, root.TraceID, s.TraceID)
			assert.Equal(t, "http.request", s.Name)
			assert.Equal(t, "client", s.Meta["span.kind"])
			assert.GreaterOrEqual(t, root.Start+root.Duration, s.Start+s.Duration)
			paths = append(paths, s.Meta["http.url"][strings.LastIndex(s.Meta["http.url"], "/"):])
		}
		wantPaths := []string{"/with-context", "/without-context"}
		if runtimeWrapped {
			wantPaths = append(wantPaths, "/response")
		}
		assert.ElementsMatchf(t, wantPaths, paths, "children of %s", root.Meta["request_id"])
	}
}

func runLambda(t *testing.T, bin, variant, payload, serverURL string) (*runtimeAPI, []byte, []byte) {
	t.Helper()
	api := newRuntimeAPI(t, invocations)
	api.waitForNext = true
	api.payload = payload
	stdout, stderr := maintest.ExecUntil(t, bin, []string{
		"LAMBDA_VARIANT=" + variant,
		"TEST_SERVER_URL=" + serverURL,
		"AWS_LAMBDA_RUNTIME_API=" + api.Address(),
		"AWS_LAMBDA_FUNCTION_NAME=mainapp",
		// Keep metrics out of the network, and the output small.
		"DD_FLUSH_TO_LOG=true",
		"DD_API_KEY=",
		"DD_SITE=",
		"DD_TRACE_STARTUP_LOGS=false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED=false",
		"DD_REMOTE_CONFIGURATION_ENABLED=false",
	}, api.done, 2*time.Minute)
	return api, stdout, stderr
}
