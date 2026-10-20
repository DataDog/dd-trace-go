// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestKratosUnaryAppSec(t *testing.T) {
	for _, block := range []bool{false, true} {
		name := "monitor"
		if block {
			name = "block"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1h")
			if block {
				t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
			}
			testutils.StartAppSec(t)
			require.True(t, instr.AppSecEnabled())
			mt := mocktracer.Start()
			defer mt.Stop()
			const method = "/Fixture/Ping"
			span, ctx := tracer.StartSpanFromContext(context.Background(), "kratos.server.request")
			ctx = context.WithValue(ctx, instrumentation.PackageGoKratosV3, ext.SpanKindServer+":"+method)
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("dd-canary", "dd-test-scanner-log", "x-client-ip", "1.2.3.4"))
			called := false
			_, err := UnaryServerInterceptor()(ctx, &fixturepb.FixtureRequest{Name: "<script>window.location;</script>"}, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ any) (any, error) {
				called = true
				assert.Equal(t, false, ctx.Value(instrumentation.PackageGoKratosV3))
				return &fixturepb.FixtureReply{Message: "passed"}, nil
			})
			span.Finish()
			finished := mt.FinishedSpans()
			require.Len(t, finished, 1, "reuse the Kratos span without adding a generic span")
			require.Equal(t, "kratos.server.request", finished[0].OperationName())
			event, ok := finished[0].Tag("_dd.appsec.json").(string)
			require.True(t, ok, "security events must be attached to the active Kratos span")
			if block {
				assert.Equal(t, codes.Aborted, status.Code(err))
				assert.False(t, called, "blocked requests must not reach the application")
				assert.Contains(t, event, "blk-001-001")
			} else {
				require.NoError(t, err)
				assert.True(t, called)
				assert.Contains(t, event, "crs-941-180")
				assert.Contains(t, event, "ua0-600-55x")
			}
		})
	}
}
