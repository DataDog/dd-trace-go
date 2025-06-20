// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.
package opensearchapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	opensearchtrace "github.com/DataDog/dd-trace-go/contrib/opensearch-project/opensearch-go.v4/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	openSearchV2Address       = "http://127.0.0.1:9212"
	openSearchV2AdminUsername = "admin"
	openSearchV2AdminPassword = "ADMIN-passw0rd"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func buildBody(t *testing.T, data interface{}) *strings.Reader {
	body, err := json.Marshal(data)
	require.NoErrorf(t, err, "failed to marshal data: #%v", data)
	return strings.NewReader(string(body))
}

func TestOpenSearchV2(t *testing.T) {
	testutils.SetGlobalServiceName(t, "global-service")
	tests := []struct {
		name              string
		options           []opensearchtrace.Option
		runTest           func(*testing.T, context.Context, *opensearchapi.Client)
		assertSpans       func(*testing.T, []*mocktracer.Span)
		expectServiceName string
	}{
		{
			name: "options",
			options: []opensearchtrace.Option{
				opensearchtrace.WithServiceName("overridden-service"),
				opensearchtrace.WithResourceNamer(func(_, _ string) string {
					return "custom-resource"
				}),
			},
			runTest: func(t *testing.T, ctx context.Context, client *opensearchapi.Client) {
				_, err := client.Cluster.Health(ctx, &opensearchapi.ClusterHealthReq{})
				require.NoError(t, err, "failed to get cluster health")
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1, "unexpected number of spans")
				span := spans[0]
				assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodGet, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/_cluster/health", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Nil(t, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
			},
			expectServiceName: "overridden-service",
		},
		{
			name:    "not found",
			options: []opensearchtrace.Option{},
			runTest: func(t *testing.T, ctx context.Context, client *opensearchapi.Client) {
				searchResp, err := client.Search(ctx, &opensearchapi.SearchReq{
					Indices: []string{"non-existent-index"},
					Body: buildBody(t, map[string]interface{}{
						"query": map[string]interface{}{
							"non-existent-field": map[string]interface{}{
								"non-existent-key": "non-existent-value",
							},
						},
					}),
				})
				require.Error(t, err, "expected error")
				require.NotNil(t, searchResp, "expected response")
				defer searchResp.Inspect().Response.Body.Close()
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1, "unexpected number of spans")
				span := spans[0]
				assert.Equal(t, "POST /non-existent-index/_search", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodPost, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/non-existent-index/_search", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Equal(t, `{"query":{"non-existent-field":{"non-existent-key":"non-existent-value"}}}`, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
				assert.Contains(t, span.Tag(ext.ErrorMsg).(string), "parsing_exception", "unexpected error message")
				assert.Contains(t, span.Tag(ext.ErrorStack).(string), "opensearch-go/v4.(*Client).Perform", "unexpected error stack")
				assert.Contains(t, span.Tag(ext.ErrorType).(string), "errorString", "unexpected error type")
			},
			expectServiceName: "global-service",
		},
		{
			name:    "create-index-index-document-get-document-delete-index",
			options: []opensearchtrace.Option{},
			runTest: func(t *testing.T, ctx context.Context, client *opensearchapi.Client) {
				createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
					Index: "opensearch-test-index",
					Body: buildBody(t, map[string]interface{}{
						"settings": map[string]interface{}{
							"index": map[string]interface{}{
								"number_of_shards": 1,
							},
						},
					}),
				})
				require.NoError(t, err, "failed to create an index")
				assert.Equal(t, createResp.Acknowledged, true)
				assert.Equal(t, createResp.Index, "opensearch-test-index")
				assert.NotNil(t, createResp.Inspect().Response, "create response is nil")
				defer createResp.Inspect().Response.Body.Close()
				doc1 := map[string]interface{}{
					"field1": "value1",
					"field2": "value2",
				}
				indexResp, err := client.Index(ctx, opensearchapi.IndexReq{
					Index:      "opensearch-test-index",
					DocumentID: "1",
					Body:       buildBody(t, doc1),
				})
				require.NoError(t, err, "failed to index")
				assert.Equal(t, "opensearch-test-index", indexResp.Index, "unexpected index name")
				assert.Equal(t, "1", indexResp.ID, "unexpected index id")
				require.NotNil(t, indexResp.Inspect().Response, "index response is nil")
				defer indexResp.Inspect().Response.Body.Close()
				getResp, err := client.Document.Get(ctx, opensearchapi.DocumentGetReq{
					Index:      "opensearch-test-index",
					DocumentID: "1",
					Params: opensearchapi.DocumentGetParams{
						Human:  true,
						Pretty: true,
					},
				})
				require.NoError(t, err, "failed to get a document")
				defer getResp.Inspect().Response.Body.Close()
				assert.Equal(t, "opensearch-test-index", getResp.Index, "unexpected index name")
				assert.Equal(t, 1, getResp.Version, "unexpected version")
				doc1JSON, err := getResp.Source.MarshalJSON()
				require.NoError(t, err, "failed to marshal source of a get response")
				doc1Map := make(map[string]interface{})
				require.NoError(t, json.Unmarshal(doc1JSON, &doc1Map), "failed to unmarshal fields")
				assert.Equal(t, doc1, doc1Map, "got an unexpected document")
				deleteResp, err := client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
					Indices: []string{"opensearch-test-index"},
				})
				assert.NoError(t, err, "failed to delete an index")
				require.NotNil(t, deleteResp.Inspect().Response, "delete response is nil")
				defer deleteResp.Inspect().Response.Body.Close()
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 4, "unexpected number of spans")
				span := spans[0]
				assert.Equal(t, "PUT /opensearch-test-index", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodPut, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/opensearch-test-index", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Equal(t, `{"settings":{"index":{"number_of_shards":1}}}`, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
				span = spans[1]
				assert.Equal(t, "PUT /opensearch-test-index/_doc/?", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodPut, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/opensearch-test-index/_doc/1", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Equal(t, `{"field1":"value1","field2":"value2"}`, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
				span = spans[2]
				assert.Equal(t, "GET /opensearch-test-index/_doc/?", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodGet, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/opensearch-test-index/_doc/1", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "human=true&pretty=true", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Nil(t, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
				span = spans[3]
				assert.Equal(t, "DELETE /opensearch-test-index", span.Tag(ext.ResourceName).(string), "unexpected resource name")
				assert.Equal(t, "opensearch.query", span.OperationName(), "unexpected operation name")
				assert.Equal(t, http.MethodDelete, span.Tag(ext.OpenSearchMethod).(string))
				assert.Equal(t, "/opensearch-test-index", span.Tag(ext.OpenSearchURL), "unexpected opensearch url")
				assert.Equal(t, "", span.Tag(ext.OpenSearchParams), "unexpected opensearch params")
				assert.Nil(t, span.Tag(ext.OpenSearchBody), "unexpected opensearch params")
			},
			expectServiceName: "global-service",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opensearchConfig := opensearch.Config{
				Addresses:         []string{openSearchV2Address},
				Username:          openSearchV2AdminUsername,
				Password:          openSearchV2AdminPassword,
				EnableDebugLogger: true,
				Logger: &opensearchtransport.TextLogger{
					Output:             os.Stdout,
					EnableRequestBody:  true,
					EnableResponseBody: true,
				},
			}
			client, err := NewClient(opensearchapi.Config{Client: opensearchConfig}, tt.options...)
			require.NoError(t, err, "failed to create an OpenSearch client")
			isHealthy := false
			for i := range 10 {
				healthResp, err := client.Cluster.Health(context.Background(), &opensearchapi.ClusterHealthReq{})
				if err == nil && healthResp.Status != "red" {
					healthResp.Inspect().Response.Body.Close()
					isHealthy = true
					break
				}
				if err == nil {
					healthResp.Inspect().Response.Body.Close()
					t.Log("retrying health check: cluster health is red")
				} else {
					t.Logf("retrying health check: %v", err)
				}
				time.Sleep(time.Duration(i) * time.Second)
			}
			require.True(t, isHealthy, "cluster is not healty even after 10 retries")
			_, err = client.Client.Metrics()
			require.NotEqual(t, opensearch.ErrTransportMissingMethodMetrics, err)
			err = client.Client.DiscoverNodes()
			require.NotEqual(t, opensearch.ErrTransportMissingMethodDiscoverNodes, err)
			mt := mocktracer.Start()
			defer mt.Stop()
			root, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
			tt.runTest(t, ctx, client)
			root.Finish()
			spans := mt.FinishedSpans()
			tt.assertSpans(t, spans[:len(spans)-1])
			for _, span := range spans {
				if span.OperationName() == "test.root" {
					continue
				}
				// The following assertions are common to all spans
				assert.Equal(t, tt.expectServiceName, span.Tag("service.name"), "span has the wrong service name")
				assert.Equal(t, "opensearch-project/opensearch-go/v4", span.Tag("component"), "span has the wrong component")
				assert.Equalf(t, "opensearch", span.Tag(ext.DBSystem), "span has the wrong %s", ext.DBSystem)
				assert.Equalf(t, "client", span.Tag(ext.SpanKind), "span has the wrong %s", ext.SpanKind)
				assert.Equalf(t, "opensearch", span.Tag(ext.SpanType), "span has the wrong %s", ext.SpanType)
				assert.NotEmptyf(t, span.Tag(ext.TargetHost).(string), "%s has an empty", ext.TargetHost)
				assert.NotEmptyf(t, span.Tag(ext.TargetPort).(string), "%s has an empty", ext.TargetPort)
			}
		})
	}
}
