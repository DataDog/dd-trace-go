// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// traced runs fn under a test.root span and returns every span the agent
// received, keyed by operation name. Each operation name must be unique.
func traced(t *testing.T, fn func(ctx context.Context)) map[string]*agenttest.Span {
	t.Helper()
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")

	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)

	root, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
	fn(ctx)
	root.Finish()
	tr.Stop()

	spans := make(map[string]*agenttest.Span)
	for _, s := range agent.(agenttest.SpanLister).Spans() {
		require.NotContains(t, spans, s.Operation, "two spans named %q", s.Operation)
		spans[s.Operation] = s
	}
	return spans
}

// requireOperations checks that spans holds exactly the given operations.
func requireOperations(t *testing.T, spans map[string]*agenttest.Span, ops ...string) {
	t.Helper()
	got := make([]string, 0, len(spans))
	for op := range spans {
		got = append(got, op)
	}
	require.ElementsMatch(t, ops, got)
}

// requireParent checks that child's parent is parent.
func requireParent(t *testing.T, spans map[string]*agenttest.Span, child, parent string) {
	t.Helper()
	require.Equalf(t, spans[parent].SpanID, spans[child].ParentID, "parent of %s", child)
}

// userMeta returns the span's meta without the tags the tracer adds itself.
func userMeta(s *agenttest.Span) map[string]string {
	meta := make(map[string]string, len(s.Meta))
	for k, v := range s.Meta {
		if k == "language" || k == "runtime-id" || strings.HasPrefix(k, "_dd.") {
			continue
		}
		meta[k] = v
	}
	return meta
}

func TestContextArgument(t *testing.T) {
	spans := traced(t, func(ctx context.Context) {
		other := startOnGoroutine("test.other")
		defer other.Finish()
		spanWithContext(ctx)
		spanWithUnnamedContext(ctx)
	})

	requireOperations(t, spans, "test.root", "test.other",
		"spanWithContext", "test.inner", "spanWithRenamedContext", "spanWithUnnamedContext")
	requireParent(t, spans, "test.other", "test.root")
	// The parent comes from the argument, not from the active span.
	requireParent(t, spans, "spanWithContext", "test.root")
	requireParent(t, spans, "spanWithUnnamedContext", "test.root")
	requireParent(t, spans, "test.inner", "spanWithContext")
	// The function body sees a context that carries the new span.
	requireParent(t, spans, "spanWithRenamedContext", "spanWithContext")
	assert.Equal(t, map[string]string{"function-name": "spanWithContext", "arg": "context"},
		userMeta(spans["spanWithContext"]))
	assert.Equal(t, map[string]string{"function-name": "spanWithUnnamedContext", "arg": "unnamed"},
		userMeta(spans["spanWithUnnamedContext"]))
}

func TestNilContextArgument(t *testing.T) {
	spans := traced(t, func(context.Context) {
		spanWithNilContext(nil)
	})

	requireOperations(t, spans, "test.root", "spanWithNilContext")
	// A nil context.Context starts a new trace.
	nilCtx := spans["spanWithNilContext"]
	assert.Zero(t, nilCtx.ParentID)
	assert.NotEqual(t, spans["test.root"].TraceID, nilCtx.TraceID)
	assert.Equal(t, map[string]string{"function-name": "spanWithNilContext", "arg": "nil"}, userMeta(nilCtx))
}

// otelc matches argument types by name, so it does not take the parent from an
// argument that only implements context.Context. The parent then comes from
// the active span.
func TestImplementingContextArgument(t *testing.T) {
	spans := traced(t, func(ctx context.Context) {
		other := startOnGoroutine("test.other")
		defer other.Finish()
		spanWithImplementingContext(&customCtx{ctx})
	})

	requireOperations(t, spans, "test.root", "test.other", "spanWithImplementingContext")
	if otelc.Enabled() {
		requireParent(t, spans, "spanWithImplementingContext", "test.other")
	} else {
		requireParent(t, spans, "spanWithImplementingContext", "test.root")
	}
	assert.Equal(t, map[string]string{"function-name": "spanWithImplementingContext", "arg": "implements"},
		userMeta(spans["spanWithImplementingContext"]))
}

func TestRequestArgument(t *testing.T) {
	spans := traced(t, func(ctx context.Context) {
		other := startOnGoroutine("test.other")
		defer other.Finish()
		spanWithRequest(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	})

	requireOperations(t, spans, "test.root", "test.other",
		"spanWithRequest", "test.inner", "spanWithRenamedContext")
	requireParent(t, spans, "spanWithRequest", "test.root")
	requireParent(t, spans, "test.inner", "spanWithRequest")
	// The function body sees a request whose context carries the new span.
	requireParent(t, spans, "spanWithRenamedContext", "spanWithRequest")
	assert.Equal(t, map[string]string{"function-name": "spanWithRequest", "arg": "request"},
		userMeta(spans["spanWithRequest"]))
}

func TestContextAndRequestArguments(t *testing.T) {
	spans := traced(t, func(ctx context.Context) {
		other, otherCtx := tracer.StartSpanFromContext(ctx, "test.other")
		defer other.Finish()
		spanWithContextAndRequest(ctx, httptest.NewRequestWithContext(otherCtx, http.MethodGet, "/", nil))
	})

	requireOperations(t, spans, "test.root", "test.other", "spanWithContextAndRequest", "spanWithRenamedContext")
	// The context argument wins over the request.
	requireParent(t, spans, "spanWithContextAndRequest", "test.root")
	// The request is still updated with the new span.
	requireParent(t, spans, "spanWithRenamedContext", "spanWithContextAndRequest")
}

func TestErrorResult(t *testing.T) {
	spans := traced(t, func(context.Context) {
		_ = errorNamed()
		_, _ = errorUnnamed()
		_ = errorSetInDefer()
		_ = errorNil()
	})

	requireOperations(t, spans, "test.root", "errorNamed", "errorUnnamed", "errorSetInDefer", "errorNil")
	for op, msg := range map[string]string{
		"errorNamed":      "named error",
		"errorUnnamed":    "unnamed error",
		"errorSetInDefer": "deferred error",
	} {
		s := spans[op]
		requireParent(t, spans, op, "test.root")
		assert.EqualValuesf(t, 1, s.Error, "error flag of %s", op)
		assert.Equalf(t, msg, s.Meta["error.message"], "error.message of %s", op)
	}
	ok := spans["errorNil"]
	requireParent(t, spans, "errorNil", "test.root")
	assert.Zero(t, ok.Error)
	assert.Equal(t, map[string]string{"function-name": "errorNil", "result": "nil"}, userMeta(ok))
}

func TestDirectiveArguments(t *testing.T) {
	spans := traced(t, func(ctx context.Context) {
		spanWithoutArgs()
		spanWithArgs()
		new(receiver).Method(ctx)
		_ = spanGeneric(ctx, 1)
	})

	requireOperations(t, spans, "test.root", "spanWithoutArgs", "custom.op", "Method", "spanGeneric")
	for _, op := range []string{"spanWithoutArgs", "custom.op", "Method", "spanGeneric"} {
		requireParent(t, spans, op, "test.root")
	}
	assert.Equal(t, map[string]string{"function-name": "spanWithoutArgs"}, userMeta(spans["spanWithoutArgs"]))
	// The first span.name wins, a repeated key keeps its last value, and quotes
	// are removed.
	assert.Equal(t, map[string]string{
		"function-name": "spanWithArgs",
		"a":             "override",
		"b":             "two words",
		"empty":         "",
	}, userMeta(spans["custom.op"]))
	assert.Equal(t, map[string]string{"function-name": "Method", "kind": "method"}, userMeta(spans["Method"]))
	assert.Equal(t, map[string]string{"function-name": "spanGeneric", "kind": "generic"}, userMeta(spans["spanGeneric"]))
}

func TestLocalNames(t *testing.T) {
	spans := traced(t, func(context.Context) {
		_ = spanWithLocals()
	})

	requireOperations(t, spans, "test.root", "spanWithLocals")
	requireParent(t, spans, "spanWithLocals", "test.root")
}

// FIXME: otelc does not expand a directive on a function literal
// (https://github.com/open-telemetry/opentelemetry-go-compile-instrumentation/issues/1602).
// Once it does, the otelc branch below fails and must be removed.
func TestFunctionLiteral(t *testing.T) {
	spans := traced(t, func(context.Context) {
		//dd:span literal:assigned other:tag
		op := func() {}
		op()
	})

	if otelc.Enabled() {
		requireOperations(t, spans, "test.root")
		return
	}
	// With no function name, the first argument's value names the span.
	requireOperations(t, spans, "test.root", "assigned")
	requireParent(t, spans, "assigned", "test.root")
	assert.Equal(t, map[string]string{"literal": "assigned", "other": "tag"}, userMeta(spans["assigned"]))
}
