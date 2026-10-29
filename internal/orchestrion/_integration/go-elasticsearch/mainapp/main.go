// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the go-elasticsearch client constructors from package
// main, which the harness suites never build. TestClientsInMain builds and
// runs it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
	elasticsearch6 "github.com/elastic/go-elasticsearch/v6"
	elasticsearch7 "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v8"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/esfake"
)

type client interface {
	Perform(*http.Request) (*http.Response, error)
}

func main() {
	srv, err := esfake.Start(nil)
	if err != nil {
		panic(err)
	}
	defer srv.Close()
	addrs := []string{srv.URL}

	perform(elasticsearch6.NewClient(elasticsearch6.Config{Addresses: addrs}))("main-v6-new-client")
	perform(elasticsearch7.NewClient(elasticsearch7.Config{Addresses: addrs}))("main-v7-new-client")
	perform(elasticsearch.NewClient(elasticsearch.Config{Addresses: addrs}))("main-v8-new-client")
	perform(elasticsearch.NewTypedClient(elasticsearch.Config{Addresses: addrs}))("main-v8-new-typed-client")
	perform(elasticsearch.NewClient(elasticsearch.Config{
		Addresses: addrs,
		Transport: elastictrace.NewRoundTripper(elastictrace.WithService("contrib.client")),
	}))("main-v8-contrib-transport")

	if err := os.Setenv("ELASTICSEARCH_URL", srv.URL); err != nil {
		panic(err)
	}
	perform(elasticsearch6.NewDefaultClient())("main-v6-new-default-client")
	perform(elasticsearch7.NewDefaultClient())("main-v7-new-default-client")
	perform(elasticsearch.NewDefaultClient())("main-v8-new-default-client")
	if err := os.Unsetenv("ELASTICSEARCH_URL"); err != nil {
		panic(err)
	}

	performAliased(addrs)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

// perform takes a constructor's results and returns a function that sends
// GET /<key> with the client.
func perform[C client](c C, err error) func(key string) {
	if err != nil {
		panic(err)
	}
	return func(key string) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/"+key, nil)
		if err != nil {
			panic(err)
		}
		res, err := c.Perform(req)
		if err != nil {
			panic(err)
		}
		_ = res.Body.Close()
	}
}
