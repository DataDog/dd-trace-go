// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

// benchHTTPClient serves requests in-process against the registered handler, skipping the
// network stack so the benchmark isolates interceptor overhead rather than socket I/O.
// It stands in for a real connect.HTTPClient because connectrpc.com/connect's AnyRequest and
// AnyResponse interfaces have unexported methods that only concrete types from that package
// can implement, so a hand-rolled fake request (as used by the grpc contrib's benchmark)
// isn't possible here.
type benchHTTPClient struct {
	handler http.Handler
}

func (c benchHTTPClient) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func BenchmarkUnaryInterceptor(b *testing.B) {
	// need to use the real tracer to get representative measurements
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	handler := connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler,
		connectrpc.WithInterceptors(NewServerInterceptor()))
	httpClient := benchHTTPClient{handler: handler}

	newClient := func(opts ...Option) *connectrpc.Client[wrapperspb.StringValue, wrapperspb.StringValue] {
		return connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
			httpClient,
			"http://connect.bench"+unaryProcedure,
			connectrpc.WithInterceptors(NewClientInterceptor(opts...)),
		)
	}
	ctx := context.Background()

	b.Run("ok", func(b *testing.B) {
		client := newClient()
		b.ReportAllocs()
		for b.Loop() {
			_, _ = client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("hello")))
		}
	})

	b.Run("ok_with_analytics_rate", func(b *testing.B) {
		client := newClient(WithAnalyticsRate(0.5))
		b.ReportAllocs()
		for b.Loop() {
			_, _ = client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("hello")))
		}
	})

	b.Run("ok_with_header_tags", func(b *testing.B) {
		client := newClient(WithHeaderTags())
		b.ReportAllocs()
		for b.Loop() {
			// Simulate a realistic amount of header traffic: a couple of application
			// headers alongside the propagation headers Datadog tracing itself adds.
			request := connectrpc.NewRequest(wrapperspb.String("hello"))
			request.Header().Set("User-Agent", "connect-go/1.16.2")
			request.Header().Set("X-Request-Id", "9219028207762307503")
			_, _ = client.CallUnary(ctx, request)
		}
	})

	b.Run("error", func(b *testing.B) {
		client := newClient()
		b.ReportAllocs()
		for b.Loop() {
			_, _ = client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("error")))
		}
	})

	b.Run("error_no_stack", func(b *testing.B) {
		client := newClient(NoDebugStack())
		b.ReportAllocs()
		for b.Loop() {
			_, _ = client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("error")))
		}
	})
}
