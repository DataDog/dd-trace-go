// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"testing"

	chiv4 "github.com/go-chi/chi"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a router is traced when chi is imported
// under another name.
type TestCaseAliasedImport struct {
	handler http.Handler
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	r := chiv4.NewRouter()
	r.Get("/alias", ok)
	tc.handler = r
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	serve(ctx, t, tc.handler, "/alias")
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return expectChiSpan("GET /alias")
}
