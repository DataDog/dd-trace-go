// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	buntdbtrace "github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buntDBGenSpans() harness.GenSpansFn {
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []buntdbtrace.Option
		if serviceOverride != "" {
			opts = append(opts, buntdbtrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		db, err := buntdbtrace.Open(":memory:", opts...)
		require.NoError(t, err)
		defer db.Close()

		err = db.Update(func(tx *buntdbtrace.Tx) error {
			_, _, err := tx.Set("key", "value", nil)
			return err
		})
		require.NoError(t, err)

		return mt.FinishedSpans()
	}
}

var tidwallBuntDB = harness.TestCase{
	Name:     instrumentation.PackageTidwallBuntDB,
	GenSpans: buntDBGenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"buntdb"},
		DDService:       []string{"buntdb"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "buntdb.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "buntdb.query", spans[0].OperationName())
	},
}
