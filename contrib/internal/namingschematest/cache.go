// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschematest

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewRedisTest creates a new test for Redis naming schema.
func NewRedisTest(genSpans GenSpansFn, defaultServiceName string) func(t *testing.T) {
	return func(t *testing.T) {
		assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "redis.command", spans[0].OperationName())
		}
		assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 1)
			assert.Equal(t, "redis.command", spans[0].OperationName())
		}
		wantServiceNameV0 := ServiceNameAssertions{
			WithDefaults:             []string{defaultServiceName},
			WithDDService:            []string{defaultServiceName},
			WithDDServiceAndOverride: []string{TestServiceOverride},
		}
		t.Run("ServiceName", NewServiceNameTest(genSpans, wantServiceNameV0))
		t.Run("SpanName", NewSpanNameTest(genSpans, assertOpV0, assertOpV1))
	}
}
