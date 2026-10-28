// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package ddlambda

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/DataDog/dd-trace-go/contrib/aws/datadog-lambda-go/v2/internal/wrapper"
)

type testEvent struct {
	Name string `json:"name"`
}

type structHandler struct {
	called *bool
}

func (h structHandler) Invoke(_ context.Context, _ []byte) ([]byte, error) {
	*h.called = true
	return []byte(`"ok"`), nil
}

// invoke wraps handler, then runs a single invocation through aws-lambda-go's own handler
// adapter, so it also checks that aws-lambda-go accepts the wrapped value.
func invoke(t *testing.T, wrapped any) []*mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{
		AwsRequestID:       "request-id",
		InvokedFunctionArn: "arn:aws:lambda:us-east-1:123456789012:function:test",
	})
	_, err := lambda.NewHandler(wrapped).Invoke(ctx, []byte(`{"name":"test"}`))
	require.NoError(t, err)
	return mt.FinishedSpans()
}

func setupWrapTest(t *testing.T) {
	t.Setenv(UniversalInstrumentation, "false")
	t.Setenv("DD_ENHANCED_METRICS", "false")
	t.Setenv(ShouldUseLogForwarderEnvVar, "true")
}

func TestWrapFunctionHandlerShapes(t *testing.T) {
	setupWrapTest(t)

	testCases := []struct {
		name    string
		handler func(called *bool) any
	}{
		{"no-args", func(called *bool) any {
			return func() error { *called = true; return nil }
		}},
		{"ctx-only", func(called *bool) any {
			return func(context.Context) { *called = true }
		}},
		{"event-only", func(called *bool) any {
			return func(ev testEvent) (string, error) { *called = ev.Name == "test"; return "ok", nil }
		}},
		{"ctx-and-event", func(called *bool) any {
			return func(_ context.Context, ev testEvent) (string, error) { *called = ev.Name == "test"; return "ok", nil }
		}},
		{"lambda.Handler", func(called *bool) any {
			return structHandler{called: called}
		}},
		{"lambda.NewHandler", func(called *bool) any {
			return lambda.NewHandler(func(_ context.Context, ev testEvent) (string, error) {
				*called = ev.Name == "test"
				return "ok", nil
			})
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			wrapped := WrapFunction(tc.handler(&called), nil)
			assert.True(t, wrapper.IsWrapped(wrapped))

			spans := invoke(t, wrapped)
			assert.True(t, called)
			require.Len(t, spans, 1)
			assert.Equal(t, "aws.lambda", spans[0].OperationName())
		})
	}
}

func TestWrapFunctionNoDoubleWrap(t *testing.T) {
	setupWrapTest(t)

	t.Run("function", func(t *testing.T) {
		once := WrapFunction(func(context.Context) error { return nil }, nil)
		twice := WrapFunction(once, nil)
		assert.Equal(t, reflect.ValueOf(once).Pointer(), reflect.ValueOf(twice).Pointer())
		assert.Len(t, invoke(t, twice), 1)
	})

	t.Run("lambda.Handler", func(t *testing.T) {
		var called bool
		once := WrapLambdaHandlerInterface(structHandler{called: &called}, nil)
		assert.Same(t, once, WrapLambdaHandlerInterface(once, nil))
		twice := WrapFunction(once, nil)
		assert.Same(t, once, twice)
		assert.Len(t, invoke(t, twice), 1)
		assert.True(t, called)
	})
}

func TestWrapFunctionInvalidHandlerPassesThrough(t *testing.T) {
	setupWrapTest(t)

	notAFunc := "not a handler"
	assert.Equal(t, notAFunc, WrapFunction(notAFunc, nil))

	tooManyArgs := func(_, _, _ int) error { return nil }
	assert.Equal(t, reflect.ValueOf(tooManyArgs).Pointer(), reflect.ValueOf(WrapFunction(tooManyArgs, nil)).Pointer())
}
