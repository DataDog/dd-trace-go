// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package opensearch_test

import (
	"log"

	opensearchtrace "github.com/DataDog/dd-trace-go/contrib/opensearch-project/opensearch-go.v4/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// To start tracing OpenSearch, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	c, err := opensearchtrace.NewDefaultClient()
	if err != nil {
		log.Fatal(err)
		return
	}
	req, err := opensearchapi.ClusterHealthReq{}.GetRequest()
	if err != nil {
		log.Fatal(err)
		return
	}
	if resp, err := c.Perform(req); err != nil {
		log.Printf(resp.Status)
		return
	}
}
