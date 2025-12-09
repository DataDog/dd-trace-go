// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package elastic_test

import (
	"context"

	"gopkg.in/olivere/elastic.v5"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/olivere/elastic.v5/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// To start tracing elastic.v5 requests, create a new TracedHTTPClient that you will
// use when initializing the elastic.Client.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	tc := elastictrace.NewHTTPClient(elastictrace.WithService("my-es-service"))
	client, _ := elastic.NewClient(
		elastic.SetURL("http://127.0.0.1:9200"),
		elastic.SetHttpClient(tc),
	)

	// Spans are emitted for all
	client.Index().
		Index("twitter").Type("tweet").Index("1").
		BodyString(`{"user": "test", "message": "hello"}`).
		Do(context.Background())

	// Use a context to pass information down the call chain
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/tweet/1"),
	)
	client.Get().
		Index("twitter").Type("tweet").Index("1").
		Do(ctx)
	root.Finish()
}
