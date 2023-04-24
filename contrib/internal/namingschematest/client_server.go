// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschematest

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewHTTPServerTest creates a new test for HTTP server naming schema.
func NewHTTPServerTest(genSpans GenSpansFn, defaultName string, opts ...Option) func(t *testing.T) {
	cfg := newConfig()
	cfg.wantServiceName[namingschema.SchemaV0] = ServiceNameAssertions{
		WithDefaults:             []string{defaultName},
		WithDDService:            []string{TestDDService},
		WithDDServiceAndOverride: []string{TestServiceOverride},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return func(t *testing.T) {
		assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.request", spans[0].OperationName())
		}
		assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "http.server.request", spans[0].OperationName())
		}
		genSpansWithInit := GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
			httptrace.InitServerSpanName()
			return genSpans(t, serviceOverride)
		})
		t.Run("ServiceName", NewServiceNameTest(genSpansWithInit, "", cfg.wantServiceName[namingschema.SchemaV0]))
		t.Run("SpanName", NewOpNameTest(genSpansWithInit, assertOpV0, assertOpV1))
	}
}
