package elastic_test

import (
	"context"

	elastictrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/olivere/elastic"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	elasticv3 "gopkg.in/olivere/elastic.v3"
	elasticv5 "gopkg.in/olivere/elastic.v5"
)

// To start tracing elastic.v5 requests, create a new TracedHTTPClient that you will
// use when initializing the elastic.Client.
func Example_v5() {
	tc := elastictrace.NewHTTPClient(elastictrace.WithServiceName("my-es-service"))
	client, _ := elasticv5.NewClient(
		elasticv5.SetURL("http://127.0.0.1:9200"),
		elasticv5.SetHttpClient(tc),
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

// To trace elastic.v3 you create a TracedHTTPClient in the same way but all requests must use
// the DoC() call to pass the request context.
func Example_v3() {
	tc := elastictrace.NewHTTPClient(elastictrace.WithServiceName("my-es-service"))
	client, _ := elasticv3.NewClient(
		elasticv3.SetURL("http://127.0.0.1:9200"),
		elasticv3.SetHttpClient(tc),
	)

	// Spans are emitted for all
	client.Index().
		Index("twitter").Type("tweet").Index("1").
		BodyString(`{"user": "test", "message": "hello"}`).
		DoC(context.Background())

	// Use a context to pass information down the call chain
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/tweet/1"),
	)
	client.Get().
		Index("twitter").Type("tweet").Index("1").
		DoC(ctx)
	root.Finish()
}
