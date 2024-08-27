// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	consul "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consultrace "github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func hashicorpConsulGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []consultrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, consultrace.WithService(serviceOverride))
		}

		mt := mocktracer.Start()
		defer mt.Stop()
		client, err := consultrace.NewClient(consul.DefaultConfig(), opts...)
		require.NoError(t, err)
		kv := client.KV()

		pair := &consul.KVPair{Key: "test.key", Value: []byte("test_value")}
		_, err = kv.Put(pair, nil)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
}

var hashicorpConsul = harness.TestCase{
	Name:     instrumentation.PackageHashicorpConsulAPI,
	GenSpans: hashicorpConsulGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"consul"},
		DDService:       []string{"consul"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "consul.command", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "consul.query", spans[0].OperationName())
	},
}
