// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chi

import (
	"context"
	"net/http"
	"testing"

	chirouter "github.com/go-chi/chi"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseShadowedAlias checks that a router is traced when the function that
// builds it has a parameter named like the package. The rules add their own
// import of chi, and a name in scope must not hide it.
type TestCaseShadowedAlias struct {
	handler http.Handler
}

func newShadowedRouter(chi string) http.Handler {
	r := chirouter.NewRouter()
	r.Get(chi, ok)
	return r
}

func (tc *TestCaseShadowedAlias) Setup(context.Context, *testing.T) {
	tc.handler = newShadowedRouter("/shadowed")
}

func (tc *TestCaseShadowedAlias) Run(ctx context.Context, t *testing.T) {
	serve(ctx, t, tc.handler, "/shadowed")
}

// ExpectedSpanCount is the chi span.
func (*TestCaseShadowedAlias) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseShadowedAlias) ExpectedTraces() trace.Traces {
	return expectChiSpan("GET /shadowed")
}
