// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fasthttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"example.com/fasthttpdep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/net"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a server started in a dependency module, not
// in the module being built, is traced.
type TestCaseDependency struct {
	addr string
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	ln := net.FreeListener(t)
	tc.addr = ln.Addr().String()
	srv := fasthttpdep.Serve(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, srv.ShutdownWithContext(ctx))
	})
}

func (tc *TestCaseDependency) Run(_ context.Context, t *testing.T) {
	resp, err := http.Get("http://" + tc.addr + "/dep")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ExpectedSpanCount pins the client span and one fasthttp server span.
func (*TestCaseDependency) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /dep",
				"type":     "http",
			},
			Meta: map[string]string{
				"component": "net/http",
				"span.kind": "client",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "http.request",
						"resource": "GET /dep",
						"type":     "web",
					},
					Meta: map[string]string{
						"component": "valyala/fasthttp",
						"span.kind": "server",
					},
				},
			},
		},
	}
}
