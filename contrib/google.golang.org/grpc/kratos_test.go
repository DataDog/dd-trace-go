// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestKratosUnaryDeduplication(t *testing.T) {
	const method = "/example.Service/Call"
	for _, kind := range []string{ext.SpanKindClient, ext.SpanKindServer} {
		for _, tc := range []struct {
			name      string
			marker    any
			wantSpans int
		}{
			{name: "grpc_only", wantSpans: 1},
			{name: "kratos_span", marker: kind + ":" + method},
			{name: "different_method", marker: kind + ":/example.Service/Other", wantSpans: 1},
			{name: "different_kind", marker: "other:" + method, wantSpans: 1},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				ctx := context.Background()
				if tc.wantSpans == 0 {
					span, spanCtx := tracer.StartSpanFromContext(ctx, "kratos.request")
					defer span.Finish()
					ctx = spanCtx
				}
				if tc.marker != nil {
					ctx = context.WithValue(ctx, instrumentation.PackageGoKratosV3, tc.marker)
				}
				var downstream context.Context
				if kind == ext.SpanKindClient {
					cc, err := grpc.NewClient("localhost:1", grpc.WithTransportCredentials(insecure.NewCredentials()))
					require.NoError(t, err)
					defer cc.Close()
					err = UnaryClientInterceptor()(ctx, method, nil, nil, cc, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
						downstream = ctx
						return nil
					})
					require.NoError(t, err)
				} else {
					_, err := UnaryServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
						downstream = ctx
						return nil, nil
					})
					require.NoError(t, err)
				}
				assert.Len(t, mt.FinishedSpans(), tc.wantSpans)
				if tc.wantSpans == 0 {
					assert.Equal(t, false, downstream.Value(instrumentation.PackageGoKratosV3), "consume the marker before downstream calls")
				}
			})
		}
	}
}

func TestKratosServerMarkerRequiresSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	const method = "/example.Service/Call"
	ctx := context.WithValue(context.Background(), instrumentation.PackageGoKratosV3, ext.SpanKindServer+":"+method)
	_, err := UnaryServerInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
		assert.Equal(t, false, ctx.Value(instrumentation.PackageGoKratosV3))
		return nil, nil
	})
	require.NoError(t, err)
	assert.Len(t, mt.FinishedSpans(), 1, "a marker alone must not bypass the generic instrumentation")
}
