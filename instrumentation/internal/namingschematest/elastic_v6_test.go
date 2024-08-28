// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"strings"
	"testing"

	elasticsearch8 "github.com/elastic/go-elasticsearch/v8"
	esapi8 "github.com/elastic/go-elasticsearch/v8/esapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var elasticV6 = harness.TestCase{
	Name: instrumentation.PackageGoElasticSearchV6,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []elastictrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, elastictrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()
		cfg := elasticsearch8.Config{
			Transport: elastictrace.NewRoundTripper(opts...),
			Addresses: []string{
				"http://127.0.0.1:9204",
			},
		}
		client, err := elasticsearch8.NewClient(cfg)
		require.NoError(t, err)

		_, err = esapi8.IndexRequest{
			Index:      "twitter",
			DocumentID: "1",
			Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
		}.Do(context.Background(), client)
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	},
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
