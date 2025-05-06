// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	redistrace "github.com/DataDog/dd-trace-go/contrib/redis/go-redis.v9/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func redisGoRedisV9GenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []redistrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, redistrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client := redistrace.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, opts...)
		st := client.Set(context.Background(), "test_key", "test_value", 0)
		require.NoError(t, st.Err())

		spans := mt.FinishedSpans()
		var span *mocktracer.Span
		for _, s := range spans {
			// pick up the redis.command span except dial
			if s.OperationName() == "redis.command" {
				span = s
			}
		}
		assert.NotNil(t, span)
		return []*mocktracer.Span{span}
	}
}

var redisGoRedisV9 = harness.TestCase{
	Name:     instrumentation.PackageRedisGoRedisV9,
	GenSpans: redisGoRedisV9GenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"redis.client"},
		DDService:       []string{harness.TestDDService},
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
