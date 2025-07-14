// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"testing"

	"github.com/globalsign/mgo/bson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mgotrace "github.com/DataDog/dd-trace-go/contrib/globalsign/mgo/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var globalsignMgo = harness.TestCase{
	Name: instrumentation.PackageGlobalsignMgo,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []mgotrace.DialOption
		if serviceOverride != "" {
			opts = append(opts, mgotrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		session, err := mgotrace.Dial("localhost:27018", opts...)
		require.NoError(t, err)
		err = session.
			DB("my_db").
			C("MyCollection").
			Insert(bson.D{bson.DocElem{Name: "entity", Value: bson.DocElem{Name: "index", Value: 0}}})
		require.NoError(t, err)

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"mongodb"},
		DDService:       []string{"mongodb"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "mongodb.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "mongodb.query", spans[0].OperationName())
	},
}
