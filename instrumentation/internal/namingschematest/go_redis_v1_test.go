// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	redistrace "github.com/DataDog/dd-trace-go/contrib/go-redis/redis/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var goRedisV1Test = harness.TestCase{
	Name: instrumentation.PackageGoRedis,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []redistrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, redistrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client := redistrace.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, opts...)
		st := client.Set("test_key", "test_value", 0)
		require.NoError(t, st.Err())

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"redis.client"},
		DDService:       []string{"redis.client"},
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
