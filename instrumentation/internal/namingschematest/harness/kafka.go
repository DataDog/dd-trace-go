// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// KafkaTestCase creates a new test case for different Kafka libraries.
func KafkaTestCase(pkg instrumentation.Package, genSpans GenSpansFn) TestCase {
	return TestCase{
		Name:     pkg,
		GenSpans: genSpans,
		WantServiceNameV0: ServiceNameAssertions{
			Defaults:        []string{"kafka", "kafka"},
			DDService:       []string{"kafka", TestDDService},
			ServiceOverride: []string{TestServiceOverride, TestServiceOverride},
		},
		AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			assert.Equal(t, "kafka.produce", spans[0].OperationName())
			assert.Equal(t, "kafka.consume", spans[1].OperationName())
		},
		AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
			require.Len(t, spans, 2)
			assert.Equal(t, "kafka.send", spans[0].OperationName())
			assert.Equal(t, "kafka.process", spans[1].OperationName())
		},
	}
}
