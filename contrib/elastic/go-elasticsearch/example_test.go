package elastic_test

import (
	"context"
	"log"
	"net/http"
	"strings"

	elastictrace "github.com/abruneau/dd-trace-go-elasticsearch"
	"github.com/elastic/go-elasticsearch"
	"github.com/elastic/go-elasticsearch/esapi"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example_v7() {
	var tp http.RoundTripper
	tp = elastictrace.NewHTTPClient(elastictrace.WithServiceName("my-es-service"))
	cfg := elasticsearch.Config{
		Transport: tp,
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
