// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	server *kratoshttp.Server
	addr   string
	called atomic.Bool
	reject bool
	status int
	otel   bool
}

func (tc *TestCase) responseStatus() int {
	if tc.reject {
		return http.StatusUnauthorized
	}
	if tc.status != 0 {
		return tc.status
	}
	return http.StatusOK
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	if tc.otel {
		internalconfig.Get().SetOTelSemanticsEnabled(true, internalconfig.OriginCode)
		httptrace.ResetCfg()
		t.Cleanup(func() {
			internalconfig.Get().SetOTelSemanticsEnabled(false, internalconfig.OriginCode)
			httptrace.ResetCfg()
		})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tc.addr = listener.Addr().String()
	tc.server = kratoshttp.NewServer(kratoshttp.Listener(listener), kratoshttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			tc.called.Store(true)
			if tc.reject {
				return nil, errors.Unauthorized("DENIED", "denied by middleware")
			}
			return next(ctx, req)
		}
	}))
	tc.server.Route("/").GET("/hello", func(ctx kratoshttp.Context) error {
		kratoshttp.SetOperation(ctx, "GET /hello")
		handler := ctx.Middleware(func(context.Context, any) (any, error) {
			return "ok", nil
		})
		reply, err := handler(ctx, nil)
		if err != nil {
			return err
		}
		if tc.responseStatus() == http.StatusNoContent {
			return ctx.String(http.StatusNoContent, "")
		}
		return ctx.JSON(tc.responseStatus(), reply)
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
	require.Equal(t, tc.responseStatus(), resp.StatusCode)
	require.True(t, tc.called.Load(), "caller server middleware must be preserved")
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	statusTag := "http.status_code"
	if tc.otel {
		statusTag = "http.response.status_code"
	}
	return trace.Traces{{Tags: map[string]any{"name": "kratos.server.request", "resource": "GET /hello", "type": "web", statusTag: strconv.Itoa(tc.responseStatus())}}}
}
