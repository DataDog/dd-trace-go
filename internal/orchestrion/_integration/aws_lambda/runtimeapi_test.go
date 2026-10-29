// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awslambda

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const functionARN = "arn:aws:lambda:us-east-1:123456789012:function:mainapp"

// runtimeAPI is a fake AWS Lambda Runtime API that serves a fixed number of
// invocations, then closes done once the function has answered all of them.
// https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html
type runtimeAPI struct {
	*httptest.Server

	done chan struct{}

	mu      sync.Mutex
	next    int
	answers int
	errors  []string
	n       int
}

func newRuntimeAPI(t *testing.T, invocations int) *runtimeAPI {
	t.Helper()
	api := &runtimeAPI{done: make(chan struct{}), n: invocations}
	api.Server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.Close)
	return api
}

// Address is the value of AWS_LAMBDA_RUNTIME_API, which has no scheme.
func (api *runtimeAPI) Address() string {
	return strings.TrimPrefix(api.URL, "http://")
}

// Errors returns the error payloads the function reported.
func (api *runtimeAPI) Errors() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.errors
}

func requestID(i int) string {
	return "request-" + strconv.Itoa(i)
}

func (api *runtimeAPI) serve(w http.ResponseWriter, r *http.Request) {
	const prefix = "/2018-06-01/runtime/invocation/"
	path, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if path == "next" {
		api.serveNext(w, r)
		return
	}
	id, kind, ok := strings.Cut(path, "/")
	if !ok || (kind != "response" && kind != "error") {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	w.WriteHeader(http.StatusAccepted)

	api.mu.Lock()
	defer api.mu.Unlock()
	if kind == "error" {
		api.errors = append(api.errors, id+": "+string(body))
	}
	api.answers++
	if api.answers == api.n {
		close(api.done)
	}
}

func (api *runtimeAPI) serveNext(w http.ResponseWriter, r *http.Request) {
	api.mu.Lock()
	i := api.next
	api.next++
	api.mu.Unlock()
	if i >= api.n {
		// No more invocations: block like the real API, until the test kills
		// the function.
		<-r.Context().Done()
		return
	}
	w.Header().Set("Lambda-Runtime-Aws-Request-Id", requestID(i))
	w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
	w.Header().Set("Lambda-Runtime-Invoked-Function-Arn", functionARN)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"name":"test"}`)
}
