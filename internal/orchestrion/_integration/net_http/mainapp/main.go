// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp calls the net/http client shorthands from package main,
// where only the otelc rules with target: main apply. Each call runs on its
// own goroutine with a context in scope, so its span only has the root span as
// parent if the rule passed that context. TestClientShorthandsInMain builds and
// runs it.
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	span, ctx := tracer.StartSpanFromContext(context.Background(), "mainapp.root")
	for _, call := range []func(context.Context, string) (*http.Response, error){get, head, post, postForm} {
		res := make(chan error, 1)
		go func() {
			resp, err := call(ctx, srv.URL)
			if err == nil {
				resp.Body.Close()
			}
			res <- err
		}()
		if err := <-res; err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	span.Finish()

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func get(ctx context.Context, base string) (*http.Response, error) {
	return http.Get(base + "/get")
}

func head(ctx context.Context, base string) (*http.Response, error) {
	return http.Head(base + "/head")
}

func post(ctx context.Context, base string) (*http.Response, error) {
	return http.Post(base+"/post", "text/plain", strings.NewReader("body"))
}

func postForm(ctx context.Context, base string) (*http.Response, error) {
	return http.PostForm(base+"/postform", url.Values{"k": {"v"}})
}
