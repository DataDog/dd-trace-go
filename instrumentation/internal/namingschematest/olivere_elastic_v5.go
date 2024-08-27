// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"testing"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/olivere/elastic.v5"
)

func olivereElasticV5GenSpans() harness.GenSpansFn {
	const elasticURL = "http://127.0.0.1:9201"
	return func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []elastictrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, elastictrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()
		tc := elastictrace.NewHTTPClient(opts...)
		client, err := elastic.NewClient(
			elastic.SetURL(elasticURL),
			elastic.SetHttpClient(tc),
			elastic.SetSniff(false),
			elastic.SetHealthcheck(false),
		)
		require.NoError(t, err)

		_, err = client.Index().
			Index("twitter").Id("1").
			Type("tweet").
			BodyString(`{"user": "test", "message": "hello"}`).
			Do(context.Background())
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
}

var olivereElasticV5 = harness.TestCase{
	Name:     instrumentation.PackageOlivereElasticV5,
	GenSpans: olivereElasticV5GenSpans(),
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"elastic.client"},
		DDService:       []string{"elastic.client"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "elasticsearch.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "elasticsearch.query", spans[0].OperationName())
	},
}
