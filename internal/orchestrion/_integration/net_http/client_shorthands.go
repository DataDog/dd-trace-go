// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseClientShorthands checks that every net/http client shorthand is
// traced and parented to the span in its caller's context. Like
// TestCaseClientGoroutine, each call runs on its own goroutine.
type TestCaseClientShorthands struct {
	base
}

func (tc *TestCaseClientShorthands) Setup(ctx context.Context, t *testing.T) {
	tc.handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tc.base.Setup(ctx, t)
}

func (tc *TestCaseClientShorthands) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	baseURL := "http://" + tc.srv.Addr
	for _, call := range []func(context.Context, string) (*http.Response, error){
		shorthandGet, shorthandHead, shorthandPost, shorthandPostForm,
	} {
		res := make(chan clientResult, 1)
		go func() {
			resp, err := call(ctx, baseURL)
			if err != nil {
				res <- clientResult{err: err}
				return
			}
			resp.Body.Close()
			res <- clientResult{status: resp.StatusCode}
		}()
		got := <-res
		require.NoError(t, got.err)
		require.Equal(t, http.StatusOK, got.status)
	}
}

// The shorthands take a ctx they don't use, so the instrumentation
// has one in scope to inject.

func shorthandGet(ctx context.Context, baseURL string) (*http.Response, error) {
	return http.Get(baseURL + "/get")
}

func shorthandHead(ctx context.Context, baseURL string) (*http.Response, error) {
	return http.Head(baseURL + "/head")
}

func shorthandPost(ctx context.Context, baseURL string) (*http.Response, error) {
	return http.Post(baseURL+"/post", "text/plain", strings.NewReader("body"))
}

func shorthandPostForm(ctx context.Context, baseURL string) (*http.Response, error) {
	return http.PostForm(baseURL+"/postform", url.Values{"k": {"v"}})
}

func (*TestCaseClientShorthands) ExpectedTraces() trace.Traces {
	children := make(trace.Traces, 0, 4)
	for _, resource := range []string{"GET /get", "HEAD /head", "POST /post", "POST /postform"} {
		children = append(children, &trace.Trace{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": resource,
				"type":     "http",
			},
			Meta: map[string]string{
				"component": "net/http",
				"span.kind": "client",
			},
		})
	}
	return trace.Traces{
		{
			Tags:     map[string]any{"name": "test.root"},
			Children: children,
		},
	}
}
