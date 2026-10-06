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
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// base holds the steps shared by every test case. Each test case only
// differs in how it builds the client.
type base struct {
	client *opensearch.Client
}

func startContainer(t *testing.T) string {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartOpenSearchTestContainer(t)
	return addr
}

// TestCase builds the client with opensearchapi.NewClient, which calls
// opensearch.NewClient inside the library.
type TestCase struct {
	base
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{startContainer(t)},
		},
	})
	require.NoError(t, err, "failed to create opensearch client")
	tc.client = client.Client
}

func (b *base) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	buildBody := func(t *testing.T, data any) *strings.Reader {
		body, err := json.Marshal(data)
		require.NoErrorf(t, err, "failed to marshal data: #%v", data)
		return strings.NewReader(string(body))
	}

	createResp, err := b.client.Do(ctx, opensearchapi.IndicesCreateReq{
		Index: "opensearch-test-index",
		Body: buildBody(t, map[string]any{
			"settings": map[string]any{
				"index": map[string]any{
					"number_of_shards": 1,
				},
			},
		}),
	}, nil)
	require.NoError(t, err, "failed to create an index")
	createResp.Body.Close()

	deleteResp, err := b.client.Do(ctx, opensearchapi.IndicesDeleteReq{
		Indices: []string{"opensearch-test-index"},
	}, nil)
	require.NoError(t, err, "failed to delete an index")
	deleteResp.Body.Close()
}

func (*base) ExpectedTraces() trace.Traces {
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
						"service":  "opensearch-go.v4.test",
						"type":     "opensearch",
					},
					Meta: map[string]string{
						"component":         "opensearch-project/opensearch-go.v4",
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
								"service":  "opensearch-go.v4.test",
								"type":     "http",
							},
							Meta: map[string]string{
								"component":   "net/http",
								"http.method": "PUT",
								"http.url":    "/opensearch-test-index",
								"span.kind":   "client",
							},
						},
					},
				},
				{
					Tags: map[string]any{
						"name":     "opensearch.query",
						"resource": "DELETE /opensearch-test-index",
						"service":  "opensearch-go.v4.test",
						"type":     "opensearch",
					},
					Meta: map[string]string{
						"component":         "opensearch-project/opensearch-go.v4",
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
								"service":  "opensearch-go.v4.test",
								"type":     "http",
							},
							Meta: map[string]string{
								"component":   "net/http",
								"http.method": "DELETE",
								"http.url":    "/opensearch-test-index",
								"span.kind":   "client",
							},
						},
					},
				},
			},
		},
	}
}
