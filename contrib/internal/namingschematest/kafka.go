// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package namingschematest

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewKafkaOpNameTest generates a new test for span kafka operation names using the naming schema versioning.
func NewKafkaOpNameTest(genSpans GenSpansFn) func(t *testing.T) {
	getKafkaSpans := func(t *testing.T, serviceOverride string) (mocktracer.Span, mocktracer.Span) {
		spans := genSpans(t, "")
		require.Len(t, spans, 2)
		return spans[0], spans[1]
	}
	return func(t *testing.T) {
		t.Run("v0", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV0)

			producerSpan, consumerSpan := getKafkaSpans(t, "")
			assert.Equal(t, "kafka.produce", producerSpan.OperationName())
			assert.Equal(t, "kafka.consume", consumerSpan.OperationName())
		})
		t.Run("v1", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV1)

			producerSpan, consumerSpan := getKafkaSpans(t, "")
			assert.Equal(t, "kafka.send", producerSpan.OperationName())
			assert.Equal(t, "kafka.process", consumerSpan.OperationName())
		})
	}
}
