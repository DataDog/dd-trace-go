// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awslambda

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

func TestLambdaCompletion(t *testing.T) {
	if !built.WithOrchestrion {
		t.Skip("requires an orchestrion-built test binary")
	}
	bin := maintest.BuildWithOrchestrion(t, "./aws_lambda/mainapp")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	for _, variant := range []string{"Streaming", "StreamingError", "Panic", "ManualPanic"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			panics := strings.Contains(variant, "Panic")
			count := invocations
			if panics {
				count = 1
			}
			api := newRuntimeAPI(t, count)
			api.waitForNext = !panics
			env := []string{
				"LAMBDA_VARIANT=" + variant,
				"TEST_SERVER_URL=" + srv.URL,
				"AWS_LAMBDA_RUNTIME_API=" + api.Address(),
				"AWS_LAMBDA_FUNCTION_NAME=mainapp",
				"DD_FLUSH_TO_LOG=true", "DD_API_KEY=", "DD_SITE=",
				"DD_TRACE_STARTUP_LOGS=false", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_REMOTE_CONFIGURATION_ENABLED=false",
			}
			var stdout, stderr []byte
			if panics {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, bin)
				cmd.Env = append(os.Environ(), env...)
				var out, errOut bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				err := cmd.Run()
				require.Error(t, err, "AWS must retain its fatal panic behavior")
				require.NoError(t, ctx.Err())
				stdout, stderr = out.Bytes(), errOut.Bytes()
				require.Lenf(t, api.Errors(), 1, "stderr: %s", stderr)
				assert.Contains(t, api.Errors()[0], `"errorMessage":"handler panic"`)
				assert.Contains(t, api.Errors()[0], `"errorType":"string"`)
				assert.Contains(t, api.Errors()[0], "panicHandler")
			} else {
				stdout, stderr = maintest.ExecUntil(t, bin, env, api.done, time.Minute)
				require.Emptyf(t, api.Errors(), "stderr: %s", stderr)
				assert.Equal(t, []string{"stream response", "stream response"}, api.Responses())
				assert.Equal(t, invocations, strings.Count(string(stdout), "response closed"))
				assert.Equal(t, invocations, strings.Count(string(stdout), `"m":"stream.metric"`))
				api.mu.Lock()
				assert.Equal(t, []string{"application/x-lambda-test", "application/x-lambda-test"}, api.contentTypes)
				for _, trailer := range api.trailers {
					if variant == "StreamingError" {
						body, err := base64.StdEncoding.DecodeString(trailer.Get("Lambda-Runtime-Function-Error-Body"))
						assert.NoError(t, err)
						assert.Contains(t, string(body), "late stream failure")
					} else {
						assert.Empty(t, trailer.Get("Lambda-Runtime-Function-Error-Body"))
					}
				}
				api.mu.Unlock()
			}
			spans := parseSpans(t, stdout)
			assert.Equal(t, count, strings.Count(string(stdout), `"m":"aws.lambda.enhanced.invocations"`))
			errorCount := 0
			if panics || variant == "StreamingError" {
				errorCount = count
			}
			assert.Equal(t, errorCount, strings.Count(string(stdout), `"m":"aws.lambda.enhanced.errors"`))
			var roots []span
			for _, s := range spans {
				if s.Name == "aws.lambda" {
					roots = append(roots, s)
				}
			}
			require.Lenf(t, roots, count, "stdout: %s\nstderr: %s", stdout, stderr)
			for _, root := range roots {
				if panics {
					assert.Contains(t, root.Meta["error.message"], "handler panic")
				} else if variant == "StreamingError" {
					assert.Contains(t, root.Meta["error.message"], "late stream failure")
				}
				if panics || variant == "StreamingError" {
					assert.Equal(t, 1, root.Error)
				} else {
					assert.Zero(t, root.Error)
				}
				if panics {
					continue
				}
				children := 0
				for _, s := range spans {
					if s.ParentID == root.SpanID && strings.HasSuffix(s.Meta["http.url"], "/during-read") {
						children++
						assert.Equal(t, root.TraceID, s.TraceID)
						assert.GreaterOrEqual(t, root.Start+root.Duration, s.Start+s.Duration)
					}
				}
				assert.Equal(t, 1, children, "work during Read must finish before the invocation flush")
			}
		})
	}
}
