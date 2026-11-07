// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// warnLogger records the warnings logged through it.
type warnLogger struct {
	warnings []string
}

func (*warnLogger) Debug(string, ...any) {}
func (*warnLogger) Info(string, ...any)  {}
func (*warnLogger) Error(string, ...any) {}

func (l *warnLogger) Warn(msg string, args ...any) {
	l.warnings = append(l.warnings, fmt.Sprintf(msg, args...))
}

func spansNamed(spans []*mocktracer.Span, operation string) []*mocktracer.Span {
	var named []*mocktracer.Span
	for _, span := range spans {
		if span.OperationName() == operation {
			named = append(named, span)
		}
	}
	return named
}

func TestInject(t *testing.T) {
	t.Run("without a span", func(t *testing.T) {
		header := make(http.Header)
		logger := new(warnLogger)
		inject(context.Background(), tracer.HTTPHeadersCarrier(header), logger, logPrefix)
		assert.Empty(t, header)
		assert.Empty(t, logger.warnings)
	})

	t.Run("with a span", func(t *testing.T) {
		startMockTracer(t)
		span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		defer span.Finish()
		header := make(http.Header)
		logger := new(warnLogger)
		inject(ctx, tracer.HTTPHeadersCarrier(header), logger, logPrefix)
		assert.Equal(t, strconv.FormatUint(span.Context().SpanID(), 10), header.Get(tracer.DefaultParentIDHeader))
		assert.Equal(t, strconv.FormatUint(span.Context().TraceIDLower(), 10), header.Get(tracer.DefaultTraceIDHeader))
		assert.Empty(t, logger.warnings)
	})

	t.Run("extracts what it injects", func(t *testing.T) {
		mt := startMockTracer(t)
		parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		carrier := tracer.TextMapCarrier{}
		inject(ctx, carrier, new(warnLogger), logPrefix)
		opts, err := propagationOptions(carrier)
		require.NoError(t, err)
		child := tracer.StartSpan("child", opts...)
		child.Finish()
		parent.Finish()
		spans := spansNamed(mt.FinishedSpans(), "child")
		require.Len(t, spans, 1)
		assert.Equal(t, parent.Context().SpanID(), spans[0].ParentID())
		assert.Equal(t, parent.Context().TraceIDLower(), spans[0].TraceID())
	})

	// The trace context propagated in other styles than the one the span continues becomes span
	// links.
	t.Run("extracts span links", func(t *testing.T) {
		startMockTracer(t)
		carrier := tracer.TextMapCarrier{
			tracer.DefaultTraceIDHeader:  "1",
			tracer.DefaultParentIDHeader: "2",
			"traceparent":                "00-00000000000000000000000000000003-0000000000000004-01",
		}
		opts, err := propagationOptions(carrier)
		require.NoError(t, err)
		require.Len(t, opts, 2)
		var cfg tracer.StartSpanConfig
		for _, opt := range opts {
			opt(&cfg)
		}
		require.NotNil(t, cfg.Parent)
		assert.NotEmpty(t, cfg.SpanLinks)
	})

	// Only a tracer that extracts reports that it found no trace context.
	for _, test := range []struct {
		name    string
		started bool
		wantErr error
	}{
		{name: "reports a carrier without a trace context", started: true, wantErr: tracer.ErrSpanContextNotFound},
		{name: "reports nothing while tracing is disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.started {
				startMockTracer(t)
			}
			opts, err := propagationOptions(tracer.TextMapCarrier{})
			assert.Nil(t, opts)
			assert.ErrorIs(t, err, test.wantErr)
		})
	}

	t.Run("logs a failure", func(t *testing.T) {
		startMockTracer(t)
		// A span without a span context cannot be injected.
		ctx := tracer.ContextWithSpan(context.Background(), new(tracer.Span))
		logger := new(warnLogger)
		inject(ctx, tracer.TextMapCarrier{}, logger, logPrefix)
		require.Len(t, logger.warnings, 1)
		assert.Regexp(t, "^contrib/connectrpc.com/connect: failed to inject trace context: .+", logger.warnings[0])
	})

	t.Run("through the configuration", func(t *testing.T) {
		startMockTracer(t)
		span, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
		defer span.Finish()
		header := make(http.Header)
		newTestConfig().Inject(ctx, tracer.HTTPHeadersCarrier(header))
		assert.Equal(t, strconv.FormatUint(span.Context().SpanID(), 10), header.Get(tracer.DefaultParentIDHeader))
	})
}
