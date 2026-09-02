// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestServerHTTP(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	parent := tracer.StartSpan("upstream")
	header := testHeader{}
	require.NoError(t, tracer.Inject(parent.Context(), headerCarrier{Header: header}))
	parent.Finish()

	req, err := http.NewRequest(http.MethodPost, "http://example.com/v1/greeters/alice", nil)
	require.NoError(t, err)
	req.Header.Set("X-Request-ID", "test-request-id")
	tr := &testTransport{
		kind:         transport.KindHTTP,
		operation:    "/helloworld.v1.Greeter/SayHello",
		header:       header,
		request:      req,
		pathTemplate: "/v1/greeters/{name}",
	}
	ctx := transport.NewServerContext(context.Background(), tr)

	var activeSpanID uint64
	next := func(ctx context.Context, req any) (any, error) {
		span, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)
		activeSpanID = span.Context().SpanID()
		return req, nil
	}
	reply, err := Server(WithHeaderTags([]string{"x-request-id"}))(next)(ctx, "reply")
	require.NoError(t, err)
	assert.Equal(t, "reply", reply)

	span := findSpan(t, mt, "http.request", ext.SpanKindServer)
	assert.Equal(t, activeSpanID, span.SpanID())
	assert.Equal(t, parent.Context().SpanID(), span.ParentID())
	assert.Equal(t, parent.Context().TraceID(), span.Context().TraceID())
	assert.Equal(t, "kratos", span.Tag(ext.ServiceName))
	assert.Equal(t, string(instrumentation.PackageGoKratosV3), span.Tag(ext.KeyServiceSource))
	assert.Equal(t, "/helloworld.v1.Greeter/SayHello", span.Tag(ext.ResourceName))
	assert.Equal(t, ext.SpanTypeWeb, span.Tag(ext.SpanType))
	assert.Equal(t, ext.SpanKindServer, span.Tag(ext.SpanKind))
	assert.Equal(t, "go-kratos/kratos.v3", span.Tag(ext.Component))
	assert.Equal(t, string(instrumentation.PackageGoKratosV3), span.Integration())
	assert.Equal(t, "http", span.Tag(ext.RPCSystem))
	assert.Equal(t, "helloworld.v1.Greeter", span.Tag(ext.RPCService))
	assert.Equal(t, "SayHello", span.Tag(ext.RPCMethod))
	assert.Equal(t, http.MethodPost, span.Tag(ext.HTTPMethod))
	assert.Equal(t, "http://example.com/v1/greeters/alice", span.Tag(ext.HTTPURL))
	assert.Equal(t, "/v1/greeters/{name}", span.Tag(ext.HTTPRoute))
	assert.Equal(t, "test-request-id", span.Tag("http.request.headers.x-request-id"))
}

func TestClientGRPC(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	parent, ctx := tracer.StartSpanFromContext(context.Background(), "parent")
	header := testHeader{}
	tr := &testTransport{
		kind:      transport.KindGRPC,
		endpoint:  "dns:///payments.internal:8443",
		operation: "/helloworld.v1.Greeter/SayHello",
		header:    header,
	}
	ctx = transport.NewClientContext(ctx, tr)

	next := func(ctx context.Context, req any) (any, error) {
		span, ok := tracer.SpanFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, strconv.FormatUint(span.Context().TraceIDLower(), 10), header.Get(tracer.DefaultTraceIDHeader))
		return req, nil
	}
	reply, err := Client()(next)(ctx, "reply")
	require.NoError(t, err)
	assert.Equal(t, "reply", reply)
	parent.Finish()

	span := findSpan(t, mt, "grpc.client", ext.SpanKindClient)
	assert.Equal(t, parent.Context().SpanID(), span.ParentID())
	assert.Equal(t, ext.AppTypeRPC, span.Tag(ext.SpanType))
	assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal(t, ext.RPCSystemGRPC, span.Tag(ext.RPCSystem))
	assert.Equal(t, "/helloworld.v1.Greeter/SayHello", span.Tag(ext.GRPCFullMethod))
	assert.Equal(t, "helloworld.v1.Greeter", span.Tag(ext.RPCService))
	assert.Equal(t, "SayHello", span.Tag(ext.RPCMethod))
	assert.Equal(t, "OK", span.Tag("grpc.code"))
	assert.Equal(t, "payments.internal", span.Tag(ext.PeerHostname))
	assert.Equal(t, "payments.internal", span.Tag(ext.TargetHost))
	assert.Equal(t, "8443", span.Tag(ext.TargetPort))
}

func TestHTTPTransportEndToEnd(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	server := kratoshttp.NewServer(kratoshttp.Middleware(Server(WithService("kratos-server"))))
	server.Route("/").GET("/hello/{name}", func(ctx kratoshttp.Context) error {
		kratoshttp.SetOperation(ctx, "/helloworld.v1.Greeter/SayHello")
		handler := ctx.Middleware(func(ctx context.Context, _ any) (any, error) {
			if _, ok := tracer.SpanFromContext(ctx); !ok {
				return nil, errors.New("active span missing from handler context")
			}
			return map[string]string{"message": "hello"}, nil
		})
		reply, err := handler(ctx, nil)
		if err != nil {
			return err
		}
		return ctx.JSON(http.StatusOK, reply)
	})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	client, err := kratoshttp.NewClient(
		context.Background(),
		kratoshttp.WithEndpoint(httpServer.URL),
		kratoshttp.WithMiddleware(Client(WithService("kratos-client"))),
	)
	require.NoError(t, err)
	defer client.Close()

	var reply map[string]string
	err = client.Invoke(
		context.Background(),
		http.MethodGet,
		"/hello/alice",
		nil,
		&reply,
		kratoshttp.Operation("/helloworld.v1.Greeter/SayHello"),
		kratoshttp.PathTemplate("/hello/{name}"),
	)
	require.NoError(t, err)
	assert.Equal(t, "hello", reply["message"])

	clientSpan := findSpan(t, mt, "http.request", ext.SpanKindClient)
	serverSpan := findSpan(t, mt, "http.request", ext.SpanKindServer)
	assert.Equal(t, clientSpan.TraceID(), serverSpan.TraceID())
	assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
	assert.Equal(t, "kratos-client", clientSpan.Tag(ext.ServiceName))
	assert.Equal(t, "kratos-server", serverSpan.Tag(ext.ServiceName))
	// Kratos middleware does not expose a successful response status. Avoid
	// reporting an incorrect 200 for valid responses such as 201 or 204.
	assert.Nil(t, clientSpan.Tag(ext.HTTPCode))
	assert.Nil(t, serverSpan.Tag(ext.HTTPCode))
}

func TestHTTPQueryStringDisabled(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		spanKind string
	}{
		{name: "server_global_opt_out", envName: envQueryStringDisabled, spanKind: ext.SpanKindServer},
		{name: "client_opt_out", envName: envClientQueryStringEnabled, spanKind: ext.SpanKindClient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envName, "false")
			if tc.envName == envQueryStringDisabled {
				t.Setenv(tc.envName, "true")
			}
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest(http.MethodGet, "http://example.com/search?q=alice&token=secret", nil)
			require.NoError(t, err)
			tr := &testTransport{kind: transport.KindHTTP, operation: "/example.v1.Search/Find", header: testHeader{}, request: req}
			ctx := context.Background()
			mw := Client()
			if tc.spanKind == ext.SpanKindServer {
				ctx = transport.NewServerContext(ctx, tr)
				mw = Server()
			} else {
				ctx = transport.NewClientContext(ctx, tr)
			}

			_, err = mw(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
			require.NoError(t, err)

			span := findSpan(t, mt, "http.request", tc.spanKind)
			assert.Equal(t, "http://example.com/search", span.Tag(ext.HTTPURL))
		})
	}
}

func TestAnalyticsConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		opts    []Option
		want    any
	}{
		{name: "environment", enabled: true, want: 1.0},
		{name: "disabled_option", enabled: true, opts: []Option{WithAnalytics(false)}},
		{name: "enabled_option", opts: []Option{WithAnalytics(true)}, want: 1.0},
		{name: "rate_option", opts: []Option{WithAnalyticsRate(0.25)}, want: 0.25},
		{name: "invalid_rate", opts: []Option{WithAnalyticsRate(2)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_KRATOS_ANALYTICS_ENABLED", strconv.FormatBool(tc.enabled))
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
			require.NoError(t, err)
			tr := &testTransport{kind: transport.KindHTTP, operation: "/example.v1.Service/Test", header: testHeader{}, request: req}
			ctx := transport.NewServerContext(context.Background(), tr)

			_, err = Server(tc.opts...)(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
			require.NoError(t, err)

			span := findSpan(t, mt, "http.request", ext.SpanKindServer)
			assert.Equal(t, tc.want, span.Tag(ext.EventSampleRate))
		})
	}
}

func TestGRPCTransportEndToEnd(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := kratosgrpc.NewServer(
		kratosgrpc.Listener(listener),
		kratosgrpc.Middleware(Server(WithService("kratos-grpc-server"))),
	)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(context.Background())
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Stop(ctx))
		require.NoError(t, <-serverDone)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := kratosgrpc.NewClient(
		ctx,
		kratosgrpc.WithEndpoint(listener.Addr().String()),
		kratosgrpc.WithMiddleware(Client(WithService("kratos-grpc-client"))),
	)
	require.NoError(t, err)
	defer conn.Close()

	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)

	clientSpan := findSpan(t, mt, "grpc.client", ext.SpanKindClient)
	serverSpan := findSpan(t, mt, "grpc.server", ext.SpanKindServer)
	assert.Equal(t, clientSpan.TraceID(), serverSpan.TraceID())
	assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
	assert.Equal(t, "kratos-grpc-client", clientSpan.Tag(ext.ServiceName))
	assert.Equal(t, "kratos-grpc-server", serverSpan.Tag(ext.ServiceName))
	assert.Equal(t, "OK", clientSpan.Tag("grpc.code"))
	assert.Equal(t, "OK", serverSpan.Tag("grpc.code"))
}

func TestErrorAndOptions(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest(http.MethodGet, "http://example.com/v1/fail", nil)
	require.NoError(t, err)
	tr := &testTransport{
		kind:      transport.KindHTTP,
		operation: "/example.v1.Service/Fail",
		header:    testHeader{},
		request:   req,
	}
	ctx := transport.NewClientContext(context.Background(), tr)
	wantErr := kratoserrors.BadRequest("INVALID_REQUEST", "invalid request")
	next := func(context.Context, any) (any, error) {
		return nil, wantErr
	}
	_, err = Client(
		WithService("kratos-client-test"),
		WithSpanOptions(tracer.Tag("custom.tag", "custom-value")),
		NoDebugStack(),
	)(next)(ctx, nil)
	require.ErrorIs(t, err, wantErr)

	span := findSpan(t, mt, "http.request", ext.SpanKindClient)
	assert.Equal(t, "kratos-client-test", span.Tag(ext.ServiceName))
	assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, span.Tag(ext.KeyServiceSource))
	assert.Equal(t, "custom-value", span.Tag("custom.tag"))
	assert.Equal(t, float64(http.StatusBadRequest), span.Tag("kratos.status_code"))
	assert.Equal(t, "INVALID_REQUEST", span.Tag("kratos.error_reason"))
	assert.Equal(t, "400", span.Tag(ext.HTTPCode))
	assert.Equal(t, wantErr.Error(), span.Tag(ext.ErrorMsg))
	assert.Empty(t, span.Tag(ext.ErrorStack))
}

func TestGRPCError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := &testTransport{
		kind:      transport.KindGRPC,
		operation: "/example.v1.Service/Find",
		header:    testHeader{},
	}
	ctx := transport.NewClientContext(context.Background(), tr)
	wantErr := kratoserrors.NotFound("NOT_FOUND", "resource not found")
	next := func(context.Context, any) (any, error) {
		return nil, wantErr
	}
	_, err := Client()(next)(ctx, nil)
	require.ErrorIs(t, err, wantErr)

	span := findSpan(t, mt, "grpc.client", ext.SpanKindClient)
	assert.Equal(t, "NotFound", span.Tag("grpc.code"))
	assert.Equal(t, float64(http.StatusNotFound), span.Tag("kratos.status_code"))
	assert.Nil(t, span.Tag(ext.HTTPCode))
}

func TestGRPCCanceledIsNotSpanError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := &testTransport{
		kind:      transport.KindGRPC,
		operation: "/example.v1.Service/Find",
		header:    testHeader{},
	}
	ctx := transport.NewClientContext(context.Background(), tr)
	wantErr := status.Error(codes.Canceled, "request canceled")

	_, err := Client()(func(context.Context, any) (any, error) {
		return nil, wantErr
	})(ctx, nil)
	require.ErrorIs(t, err, wantErr)

	span := findSpan(t, mt, "grpc.client", ext.SpanKindClient)
	assert.Equal(t, codes.Canceled.String(), span.Tag("grpc.code"))
	assert.Nil(t, span.Tag(ext.ErrorMsg))
}

func TestHTTPServerClientErrorIsNotSpanError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	req, err := http.NewRequest(http.MethodGet, "http://example.com/missing", nil)
	require.NoError(t, err)
	tr := &testTransport{
		kind:      transport.KindHTTP,
		operation: "/example.v1.Service/Find",
		header:    testHeader{},
		request:   req,
	}
	ctx := transport.NewServerContext(context.Background(), tr)
	wantErr := kratoserrors.NotFound("NOT_FOUND", "resource not found")

	_, err = Server()(func(context.Context, any) (any, error) {
		return nil, wantErr
	})(ctx, nil)
	require.ErrorIs(t, err, wantErr)

	span := findSpan(t, mt, "http.request", ext.SpanKindServer)
	assert.Equal(t, "404", span.Tag(ext.HTTPCode))
	assert.Equal(t, float64(http.StatusNotFound), span.Tag("kratos.status_code"))
	assert.Equal(t, "NOT_FOUND", span.Tag("kratos.error_reason"))
	assert.Nil(t, span.Tag(ext.ErrorMsg))
}

func TestHTTPConfiguredErrorStatuses(t *testing.T) {
	tests := []struct {
		name       string
		spanKind   string
		envName    string
		envValue   string
		statusCode int
		opts       []Option
	}{
		{name: "server_custom_404", spanKind: ext.SpanKindServer, envName: envServerErrorStatuses, envValue: "400-499", statusCode: http.StatusNotFound},
		{name: "server_option_404", spanKind: ext.SpanKindServer, envName: envServerErrorStatuses, envValue: "", statusCode: http.StatusNotFound, opts: []Option{WithStatusCheck(func(int) bool { return true })}},
		{name: "client_default_503", spanKind: ext.SpanKindClient, envName: envClientErrorStatuses, envValue: "", statusCode: http.StatusServiceUnavailable},
		{name: "client_custom_503", spanKind: ext.SpanKindClient, envName: envClientErrorStatuses, envValue: "500-599", statusCode: http.StatusServiceUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envName, tc.envValue)
			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
			require.NoError(t, err)
			tr := &testTransport{kind: transport.KindHTTP, operation: "/example.v1.Service/Test", header: testHeader{}, request: req}
			ctx := context.Background()
			mw := Client(tc.opts...)
			if tc.spanKind == ext.SpanKindServer {
				ctx = transport.NewServerContext(ctx, tr)
				mw = Server(tc.opts...)
			} else {
				ctx = transport.NewClientContext(ctx, tr)
			}
			wantErr := kratoserrors.New(tc.statusCode, "TEST_ERROR", "test error")

			_, err = mw(func(context.Context, any) (any, error) { return nil, wantErr })(ctx, nil)
			require.ErrorIs(t, err, wantErr)

			span := findSpan(t, mt, "http.request", tc.spanKind)
			assert.Equal(t, strconv.Itoa(tc.statusCode), span.Tag(ext.HTTPCode))
			if tc.name == "client_default_503" {
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			} else {
				assert.Equal(t, wantErr.Error(), span.Tag(ext.ErrorMsg))
			}
		})
	}
}

func TestNamingSchema(t *testing.T) {
	t.Cleanup(instrumentation.ReloadConfig)

	tests := []struct {
		name       string
		kind       transport.Kind
		spanKind   string
		middleware middleware.Middleware
		wantV0     string
		wantV1     string
	}{
		{name: "http_server", kind: transport.KindHTTP, spanKind: ext.SpanKindServer, middleware: Server(), wantV0: "http.request", wantV1: "http.server.request"},
		{name: "http_client", kind: transport.KindHTTP, spanKind: ext.SpanKindClient, middleware: Client(), wantV0: "http.request", wantV1: "http.client.request"},
		{name: "grpc_server", kind: transport.KindGRPC, spanKind: ext.SpanKindServer, middleware: Server(), wantV0: "grpc.server", wantV1: "grpc.server.request"},
		{name: "grpc_client", kind: transport.KindGRPC, spanKind: ext.SpanKindClient, middleware: Client(), wantV0: "grpc.client", wantV1: "grpc.client.request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, schema := range []struct {
				name string
				want string
			}{
				{name: "v0", want: tc.wantV0},
				{name: "v1", want: tc.wantV1},
			} {
				t.Run(schema.name, func(t *testing.T) {
					t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", schema.name)
					instrumentation.ReloadConfig()

					mt := mocktracer.Start()
					defer mt.Stop()
					req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
					require.NoError(t, err)
					tr := &testTransport{kind: tc.kind, operation: "/example.v1.Service/Test", header: testHeader{}, request: req}
					ctx := context.Background()
					if tc.spanKind == ext.SpanKindServer {
						ctx = transport.NewServerContext(ctx, tr)
					} else {
						ctx = transport.NewClientContext(ctx, tr)
					}
					_, err = tc.middleware(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
					require.NoError(t, err)
					findSpan(t, mt, schema.want, tc.spanKind)
				})
			}
		})
	}
}

func TestDefaultServiceNameUsesDDService(t *testing.T) {
	// Applications commonly construct middleware before starting the tracer.
	// Keep these instances across the configuration reload to verify that the
	// default service name is resolved lazily when a span starts.
	middlewares := map[string]struct {
		kind       transport.Kind
		spanKind   string
		middleware middleware.Middleware
	}{
		"server": {kind: transport.KindHTTP, spanKind: ext.SpanKindServer, middleware: Server()},
		"client": {kind: transport.KindHTTP, spanKind: ext.SpanKindClient, middleware: Client()},
	}

	t.Cleanup(instrumentation.ReloadConfig)
	t.Setenv("DD_SERVICE", "checkout-api")
	t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
	instrumentation.ReloadConfig()

	mt := mocktracer.Start()
	defer mt.Stop()

	for name, tc := range middlewares {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
			require.NoError(t, err)
			tr := &testTransport{kind: tc.kind, operation: "/example.v1.Service/Test", header: testHeader{}, request: req}
			ctx := context.Background()
			if tc.spanKind == ext.SpanKindServer {
				ctx = transport.NewServerContext(ctx, tr)
			} else {
				ctx = transport.NewClientContext(ctx, tr)
			}
			_, err = tc.middleware(func(context.Context, any) (any, error) { return nil, nil })(ctx, nil)
			require.NoError(t, err)

			span := findSpan(t, mt, "http.request", tc.spanKind)
			assert.Equal(t, "checkout-api", span.Tag(ext.ServiceName))
		})
	}
}

func TestCachedServiceName(t *testing.T) {
	tracer.Stop()
	calls := 0
	serviceName := newCachedServiceName(func() string {
		calls++
		return "checkout-api"
	})
	assert.Equal(t, "checkout-api", serviceName.String())
	assert.Equal(t, 2, calls, "service name must not be cached before tracer initialization")

	tracer.Start(tracer.WithAgentAddr("127.0.0.1:0"))
	t.Cleanup(tracer.Stop)
	serviceName = newCachedServiceName(func() string {
		calls++
		return "payments-api"
	})
	assert.Equal(t, "payments-api", serviceName.String())
	assert.Equal(t, 3, calls, "service name must be cached after tracer initialization")
}

func TestWithoutTransportContext(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	next := func(_ context.Context, req any) (any, error) {
		return req, nil
	}
	for name, mw := range map[string]middleware.Middleware{
		"server": Server(),
		"client": Client(),
	} {
		t.Run(name, func(t *testing.T) {
			reply, err := mw(next)(context.Background(), name)
			require.NoError(t, err)
			assert.Equal(t, name, reply)
		})
	}
	assert.Empty(t, mt.FinishedSpans())
}

func TestSplitOperation(t *testing.T) {
	for _, tc := range []struct {
		operation string
		service   string
		method    string
	}{
		{operation: "/helloworld.v1.Greeter/SayHello", service: "helloworld.v1.Greeter", method: "SayHello"},
		{operation: "health", service: "health"},
		{},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			service, method := splitOperation(tc.operation)
			assert.Equal(t, tc.service, service)
			assert.Equal(t, tc.method, method)
		})
	}
}

func TestResourceName(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com/users/alice", nil)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		tr   *testTransport
		want string
	}{
		{name: "operation", tr: &testTransport{operation: "/example.v1.Service/Get"}, want: "/example.v1.Service/Get"},
		{name: "http_method_and_template", tr: &testTransport{kind: transport.KindHTTP, request: req, pathTemplate: "/users/{name}"}, want: "GET /users/{name}"},
		{name: "http_without_template", tr: &testTransport{kind: transport.KindHTTP, request: req}, want: "unknown"},
		{name: "grpc_without_operation", tr: &testTransport{kind: transport.KindGRPC}, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resourceName(tc.tr))
		})
	}
}

func TestEndpointHostPort(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		host     string
		port     string
	}{
		{endpoint: "localhost:50051", host: "localhost", port: "50051"},
		{endpoint: "dns:///payments.internal:8443", host: "payments.internal", port: "8443"},
		{endpoint: "dns://payments.internal:8443", host: "payments.internal", port: "8443"},
		{endpoint: "dns://resolver.example/payments.internal:8443", host: "payments.internal", port: "8443"},
		{endpoint: "dns://resolver.example/[2001:db8::1]:8443", host: "2001:db8::1", port: "8443"},
		{endpoint: "dns:///payments.internal", host: "payments.internal"},
		{endpoint: "dns:///%zz"},
		{endpoint: "unix:///tmp/kratos.sock"},
		{},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			host, port := endpointHostPort(tc.endpoint)
			assert.Equal(t, tc.host, host)
			assert.Equal(t, tc.port, port)
		})
	}
}

func TestHeaderCarrierStopsOnError(t *testing.T) {
	wantErr := errors.New("stop iteration")
	carrier := headerCarrier{Header: testHeader{"x-test": {"first", "second"}}}
	var values []string

	err := carrier.ForeachKey(func(_ string, value string) error {
		values = append(values, value)
		if value == "second" {
			return wantErr
		}
		return nil
	})

	require.ErrorIs(t, err, wantErr)
	assert.Equal(t, []string{"first", "second"}, values)
}

func BenchmarkHTTPServerMiddleware(b *testing.B) {
	tracer.Start(tracer.WithLogger(discardLogger{}))
	defer tracer.Stop()

	req, err := http.NewRequest(http.MethodGet, "http://example.com/v1/greeters/alice", nil)
	require.NoError(b, err)
	tr := &testTransport{
		kind:         transport.KindHTTP,
		operation:    "/helloworld.v1.Greeter/SayHello",
		header:       testHeader{},
		request:      req,
		pathTemplate: "/v1/greeters/{name}",
	}
	ctx := transport.NewServerContext(context.Background(), tr)
	baseline := func(_ context.Context, req any) (any, error) {
		return req, nil
	}
	instrumented := Server()(baseline)

	b.Run("baseline", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := baseline(ctx, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("instrumented", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := instrumented(ctx, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}

type discardLogger struct{}

func (discardLogger) Log(string) {}

func findSpan(t *testing.T, mt mocktracer.Tracer, operation, spanKind string) *mocktracer.Span {
	t.Helper()
	for _, span := range mt.FinishedSpans() {
		if span.OperationName() == operation && span.Tag(ext.SpanKind) == spanKind {
			return span
		}
	}
	require.FailNow(t, "span not found", "operation: %s, span.kind: %s", operation, spanKind)
	return nil
}

type testHeader map[string][]string

func (h testHeader) Get(key string) string {
	values := h.Values(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (h testHeader) Set(key, value string) {
	h[key] = []string{value}
}

func (h testHeader) Add(key, value string) {
	h[key] = append(h[key], value)
}

func (h testHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

func (h testHeader) Values(key string) []string {
	return h[key]
}

type testTransport struct {
	kind         transport.Kind
	endpoint     string
	operation    string
	header       testHeader
	request      *http.Request
	pathTemplate string
}

var _ kratoshttp.Transporter = (*testTransport)(nil)

func (tr *testTransport) Kind() transport.Kind            { return tr.kind }
func (tr *testTransport) Endpoint() string                { return tr.endpoint }
func (tr *testTransport) Operation() string               { return tr.operation }
func (tr *testTransport) RequestHeader() transport.Header { return tr.header }
func (tr *testTransport) ReplyHeader() transport.Header   { return nil }
func (tr *testTransport) Request() *http.Request          { return tr.request }
func (tr *testTransport) PathTemplate() string            { return tr.pathTemplate }
