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

// NewMemcachedOpNameTest generates a new test for memcached span operation names using the naming schema versioning.
func NewMemcachedOpNameTest(genSpans GenSpansFn) func(t *testing.T) {
	getSpan := func(t *testing.T) mocktracer.Span {
		spans := genSpans(t, "")
		require.Len(t, spans, 1)
		return spans[0]
	}
	return func(t *testing.T) {
		t.Run("v0", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV0)

			span := getSpan(t)
			assert.Equal(t, "memcached.query", span.OperationName())
		})
		t.Run("v1", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV1)

			span := getSpan(t)
			assert.Equal(t, "memcached.command", span.OperationName())
		})
	}
}
