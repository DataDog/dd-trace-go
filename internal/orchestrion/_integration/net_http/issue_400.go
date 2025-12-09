// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"testing"
)

// TestCaseIssue400 tests regressions for https://github.com/DataDog/orchestrion/issues/400.
type TestCaseIssue400 struct {
	base
}

type handlerFunc func(http.ResponseWriter, *http.Request)

// This issue happened because we used to replace with WrapHandlerFunc every time we saw a function that matched the
// signature func(w http.ResponseWriter, req *http.Request), and using a custom type like we do here broke the build.
func wrapCustomType(f func(http.ResponseWriter, *http.Request)) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f(w, r)
	}
}

func (tc *TestCaseIssue400) Setup(ctx context.Context, t *testing.T) {
	handleHit := wrapCustomType(tc.handleHit)
	handleRoot := wrapCustomType(tc.handleRoot)
	mux := http.NewServeMux()
	mux.HandleFunc("/hit", handleHit)
	mux.HandleFunc("/", handleRoot)
	tc.handler = mux

	tc.base.Setup(ctx, t)
}
