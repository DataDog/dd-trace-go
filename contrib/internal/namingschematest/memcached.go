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

// NewMemcachedOpNameTest generates a new test for memcached span operation names using the naming schema versioning.
func NewMemcachedOpNameTest(genSpans GenSpansFn) func(t *testing.T) {
	assertV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.query", spans[0].OperationName())
	}
	assertV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.command", spans[0].OperationName())
	}
	return NewOpNameTest(genSpans, assertV0, assertV1)
}
