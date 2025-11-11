// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

//go:build linux || !githubci

package opensearch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testopensearch "github.com/testcontainers/testcontainers-go/modules/opensearch"
)

type TestCase struct {
	client *opensearchapi.Client
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	opensearchContainer, err := testopensearch.Run(ctx, "public.ecr.aws/opensearchproject/opensearch:2")
	require.NoError(t, err, "failed to run opensearch container")
	require.NoError(t, opensearchContainer.Start(ctx), "failed to start opensearch container")
	endpoint, err := opensearchContainer.Endpoint(ctx, "http")
	require.NoError(t, err, "failed to get opensearch container endpoint")
	tc.client, err = opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{endpoint},
		},
	})
	require.NoError(t, err, "failed to create opensearch client")
	t.Cleanup(func() { require.NoError(t, opensearchContainer.Terminate(context.Background())) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	buildBody := func(t *testing.T, data interface{}) *strings.Reader {
		body, err := json.Marshal(data)
		require.NoErrorf(t, err, "failed to marshal data: #%v", data)
		return strings.NewReader(string(body))
	}

	createResp, err := tc.client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: "opensearch-test-index",
		Body: buildBody(t, map[string]interface{}{
			"settings": map[string]interface{}{
				"index": map[string]interface{}{
					"number_of_shards": 1,
				},
			},
		}),
	})
	assert.NoError(t, err, "failed to create an index")
	createResp.Inspect().Response.Body.Close()

	deleteResp, err := tc.client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
		Indices: []string{"opensearch-test-index"},
	})
	assert.NoError(t, err, "failed to delete an index")
	deleteResp.Inspect().Response.Body.Close()
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "opensearch.query",
						"resource": "PUT /opensearch-test-index",
						"service":  "opensearch.client",
						"type":     "opensearch",
					},
					Meta: map[string]string{
						"_dd.base_service":  "opensearch-go.v4.test",
						"component":         "opensearch-project/opensearch-go/v4",
						"db.system":         "opensearch",
						"opensearch.method": "PUT",
						"opensearch.body":   `{"settings":{"index":{"number_of_shards":1}}}`,
						"opensearch.params": "",
						"opensearch.url":    "/opensearch-test-index",
						"span.kind":         "client",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "PUT /opensearch-test-index",
								"service":  "opensearch.client",
								"type":     "http",
							},
							Meta: map[string]string{
								"_dd.base_service": "opensearch-go.v4.test",
								"component":        "net/http",
								"http.method":      "PUT",
								"http.url":         "/opensearch-test-index",
								"span.kind":        "client",
							},
						},
					},
				},
				{
					Tags: map[string]any{
						"name":     "opensearch.query",
						"resource": "DELETE /opensearch-test-index",
						"service":  "opensearch.client",
						"type":     "opensearch",
					},
					Meta: map[string]string{
						"_dd.base_service":  "opensearch-go.v4.test",
						"component":         "opensearch-project/opensearch-go/v4",
						"db.system":         "opensearch",
						"opensearch.method": "DELETE",
						"opensearch.params": "",
						"opensearch.url":    "/opensearch-test-index",
						"span.kind":         "client",
					},
					Children: trace.Traces{
						{
							Tags: map[string]any{
								"name":     "http.request",
								"resource": "DELETE /opensearch-test-index",
								"service":  "opensearch.client",
								"type":     "http",
							},
							Meta: map[string]string{
								"_dd.base_service": "opensearch-go.v4.test",
								"component":        "net/http",
								"http.method":      "DELETE",
								"http.url":         "/opensearch-test-index",
								"span.kind":        "client",
							},
						},
					},
				},
			},
		},
	}
}
