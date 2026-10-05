// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	valkeytrace "github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var valkeyTest = harness.TestCase{
	Name: instrumentation.PackageValkeyIoValkeyGo,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []valkeytrace.Option
		if serviceOverride != "" {
			opts = append(opts, valkeytrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client, err := valkeytrace.NewClient(valkey.ClientOption{
			InitAddress:  []string{"127.0.0.1:6380"},
			Password:     "password-for-default",
			DisableCache: true,
		}, opts...)
		require.NoError(t, err)
		defer client.Close()

		ctx := context.Background()
		cmd := client.B().Set().Key("test_key").Value("test_value").Build()
		err = client.Do(ctx, cmd).Error()
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"valkey.client"},
		DDService:       []string{harness.TestDDService},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	WantServiceSource: harness.ServiceSourceAssertions{
		Defaults:        []string{string(instrumentation.PackageValkeyIoValkeyGo)},
		ServiceOverride: []string{instrumentation.ServiceSourceWithServiceOption},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "valkey.command", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "valkey.command", spans[0].OperationName())
	},
}
