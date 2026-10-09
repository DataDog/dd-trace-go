// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"net"
	"net/http"
	"testing"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	server *kratoshttp.Server
	addr   string
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = listener.Addr().String()
	tc.server = kratoshttp.NewServer(kratoshttp.Listener(listener))
	tc.server.Route("/").GET("/hello", func(ctx kratoshttp.Context) error {
		kratoshttp.SetOperation(ctx, "GET /hello")
		handler := ctx.Middleware(func(context.Context, any) (any, error) {
			return "ok", nil
		})
		reply, err := handler(ctx, nil)
		if err != nil {
			return err
		}
		return ctx.JSON(http.StatusOK, reply)
	})
	go func() { _ = tc.server.Start(ctx) }()
	t.Cleanup(func() { require.NoError(t, tc.server.Stop(context.Background())) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+tc.addr+"/hello", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{{Tags: map[string]any{"name": "kratos.server.request", "resource": "GET /hello", "type": "web"}}}
}
