// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package opensearchapi_test

import (
	"context"
	"log"

	opensearchapitrace "github.com/DataDog/dd-trace-go/contrib/opensearch-project/opensearch-go.v4/v2/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// To start tracing OpenSearch, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	c, err := opensearchapitrace.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
		},
	})
	if err != nil {
		log.Fatal(err)
		return
	}
	if resp, err := c.Cluster.Health(context.Background(), &opensearchapi.ClusterHealthReq{}); err != nil {
		log.Printf(resp.Status)
		return
	}
}
