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

// NewKafkaTest creates a new test for Kafka naming schema.
func NewKafkaTest(genSpans GenSpansFn) func(t *testing.T) {
	return func(t *testing.T) {
		assertOpV0 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 2)
			assert.Equal(t, "kafka.produce", spans[0].OperationName())
			assert.Equal(t, "kafka.consume", spans[1].OperationName())
		}
		assertOpV1 := func(t *testing.T, spans []mocktracer.Span) {
			require.Len(t, spans, 2)
			assert.Equal(t, "kafka.send", spans[0].OperationName())
			assert.Equal(t, "kafka.process", spans[1].OperationName())
		}
		wantServiceNameV0 := ServiceNameAssertions{
			WithDefaults:             []string{"kafka", "kafka"},
			WithDDService:            []string{"kafka", TestDDService},
			WithDDServiceAndOverride: []string{TestServiceOverride, TestServiceOverride},
		}
		t.Run("ServiceName", NewServiceNameTest(genSpans, "kafka", wantServiceNameV0))
		t.Run("SpanName", NewOpNameTest(genSpans, assertOpV0, assertOpV1))
	}
}
