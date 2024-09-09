// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redigotrace "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var redigoTest = harness.TestCase{
	Name: instrumentation.PackageRedigo,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []interface{}
		if serviceOverride != "" {
			opts = append(opts, redigotrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		c, err := redigotrace.Dial("tcp", "127.0.0.1:6379", opts...)
		require.NoError(t, err)
		_, err = c.Do("SET", "test_key", "test_value")
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"redis.conn"},
		DDService:       []string{"redis.conn"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "redis.command", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "redis.command", spans[0].OperationName())
	},
}
