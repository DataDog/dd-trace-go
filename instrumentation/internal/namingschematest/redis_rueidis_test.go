// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rueidistrace "github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var redisRueidisTest = harness.TestCase{
	Name: instrumentation.PackageRedisRueidis,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []rueidistrace.Option
		if serviceOverride != "" {
			opts = append(opts, rueidistrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client, err := rueidistrace.NewClient(rueidis.ClientOption{
			InitAddress: []string{"127.0.0.1:6379"},
		}, opts...)
		require.NoError(t, err)
		defer client.Close()

		ctx := context.Background()
		client.Do(ctx, client.B().Set().Key("rueidis_key").Value("rueidis_val").Build())

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"redis.client"},
		DDService:       []string{harness.TestDDService},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageRedisRueidis)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
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
