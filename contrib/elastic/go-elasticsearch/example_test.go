// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.
package elastic_test

import (
	"context"
	"log"
	"strings"

	"github.com/elastic/go-elasticsearch"
	"github.com/elastic/go-elasticsearch/esapi"
	elastictrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/elastic/go-elasticsearch"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example_v7() {
	cfg := elasticsearch.Config{
		Transport: elastictrace.NewRoundTripper(elastictrace.WithServiceName("my-es-service")),
		Addresses: []string{
			"http://127.0.0.1:9200",
		},
	}
	es, err := elasticsearch.NewClient(cfg)
	if err != nil {
		log.Fatalf("Error creating the client: %s", err)
	}

	_, err = esapi.IndexRequest{
		Index:      "twitter",
		DocumentID: "1",
		Body:       strings.NewReader(`{"user": "test", "message": "hello"}`),
	}.Do(context.Background(), es)

	if err != nil {
		log.Fatalf("Error creating index: %s", err)
	}

	// Use a context to pass information down the call chain
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/tweet/1"),
	)

	_, err = esapi.GetRequest{
		Index:      "twitter",
		DocumentID: "1",
	}.Do(ctx, es)

	if err != nil {
		log.Fatalf("Error getting index: %s", err)
	}

	root.Finish()

}
