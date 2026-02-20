// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProcedure = "/test.v1.TestService/Ping"
)

// jsonCodec is a simple JSON codec for testing without protobuf.
type jsonCodec struct{}

func (jsonCodec) Name() string                   { return "json" }
func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// testRequest is a simple type used as a Connect request/response.
type testRequest struct {
	Name string `json:"name"`
}

// testResponse is a simple type used as a Connect response.
type testResponse struct {
	Message string `json:"message"`
}

var jsonCodecOpt = connect.WithCodec(jsonCodec{})

// newTestServer creates an httptest.Server with a unary handler and the given interceptor options.
func newTestServer(t *testing.T, handler func(context.Context, *connect.Request[testRequest]) (*connect.Response[testResponse], error), opts ...Option) *httptest.Server {
	t.Helper()
	interceptor := NewInterceptor(opts...)
	mux := http.NewServeMux()
	mux.Handle(testProcedure, connect.NewUnaryHandler(
		testProcedure,
		handler,
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient creates a Connect client pointing to the given URL with the given interceptor options.
func newTestClient(url string, opts ...Option) *connect.Client[testRequest, testResponse] {
	interceptor := NewInterceptor(opts...)
	return connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		url+testProcedure,
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	)
}

func TestUnary(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, req *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "hello " + req.Msg.Name}), nil
	})

	client := newTestClient(srv.URL)
	resp, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "world"}))
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp.Msg.Message)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	// Find server and client spans
	var serverSpan, clientSpan *mocktracer.Span
	for _, s := range spans {
		switch s.Tag(ext.SpanKind) {
		case ext.SpanKindServer:
			serverSpan = s
		case ext.SpanKindClient:
			clientSpan = s
		}
	}
	require.NotNil(t, serverSpan)
	require.NotNil(t, clientSpan)

	// Verify server span
	assert.Equal(t, testProcedure, serverSpan.Tag(tagMethodName))
	assert.Equal(t, methodKindUnary, serverSpan.Tag(tagMethodKind))
	assert.Equal(t, ext.RPCSystemConnectRPC, serverSpan.Tag(ext.RPCSystem))
	assert.Equal(t, "test.v1.TestService", serverSpan.Tag(ext.RPCService))
	assert.Equal(t, "Ping", serverSpan.Tag(ext.RPCMethod))
	assert.Equal(t, componentName, serverSpan.Tag(ext.Component))

	// Verify client span
	assert.Equal(t, testProcedure, clientSpan.Tag(tagMethodName))
	assert.Equal(t, methodKindUnary, clientSpan.Tag(tagMethodKind))
	assert.Equal(t, ext.RPCSystemConnectRPC, clientSpan.Tag(ext.RPCSystem))
	assert.Equal(t, "test.v1.TestService", clientSpan.Tag(ext.RPCService))
	assert.Equal(t, "Ping", clientSpan.Tag(ext.RPCMethod))
	assert.Equal(t, componentName, clientSpan.Tag(ext.Component))
}

func TestUnaryError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	})

	client := newTestClient(srv.URL)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	var serverSpan *mocktracer.Span
	for _, s := range spans {
		if s.Tag(ext.SpanKind) == ext.SpanKindServer {
			serverSpan = s
			break
		}
	}
	require.NotNil(t, serverSpan)
	assert.NotNil(t, serverSpan.Tag(ext.ErrorMsg))
	assert.Equal(t, connect.CodeNotFound.String(), serverSpan.Tag(tagCode))
}

func TestDistributedTracing(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	})

	client := newTestClient(srv.URL)
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "trace"}))
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	var serverSpan, clientSpan *mocktracer.Span
	for _, s := range spans {
		switch s.Tag(ext.SpanKind) {
		case ext.SpanKindServer:
			serverSpan = s
		case ext.SpanKindClient:
			clientSpan = s
		}
	}
	require.NotNil(t, serverSpan)
	require.NotNil(t, clientSpan)

	// Verify trace propagation: server span should be child of client span
	assert.Equal(t, clientSpan.TraceID(), serverSpan.TraceID())
	assert.Equal(t, clientSpan.SpanID(), serverSpan.ParentID())
}

func TestNonErrorCodes(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}, NonErrorCodes(connect.CodeNotFound))

	client := newTestClient(srv.URL, NonErrorCodes(connect.CodeNotFound))
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	for _, s := range spans {
		assert.Nil(t, s.Tag(ext.ErrorMsg), "span %s should not have error tag", s.OperationName())
	}
}

func TestUntracedMethods(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	}, WithUntracedMethods(testProcedure))

	client := newTestClient(srv.URL, WithUntracedMethods(testProcedure))
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 0)
}

func TestWithService(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	svcName := "my-custom-service"
	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	}, WithService(svcName))

	client := newTestClient(srv.URL, WithService(svcName))
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	for _, s := range spans {
		assert.Equal(t, svcName, s.Tag(ext.ServiceName))
	}
}

func TestHeaderTags(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	}, WithHeaderTags())

	interceptor := NewInterceptor(WithHeaderTags())
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+testProcedure,
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	)

	req := connect.NewRequest(&testRequest{Name: "test"})
	req.Header().Set("X-Custom-Header", "custom-value")
	_, err := client.CallUnary(context.Background(), req)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	// Verify client span has the custom header tag
	for _, s := range spans {
		if s.Tag(ext.SpanKind) == ext.SpanKindClient {
			assert.Equal(t, "custom-value", s.Tag(tagHeaderPrefix+"x-custom-header"))
		}
	}
}

func TestIgnoredHeaders(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	}, WithHeaderTags(), WithIgnoredHeaders("x-secret"))

	interceptor := NewInterceptor(WithHeaderTags(), WithIgnoredHeaders("x-secret"))
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+testProcedure,
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	)

	req := connect.NewRequest(&testRequest{Name: "test"})
	req.Header().Set("X-Secret", "secret-value")
	req.Header().Set("X-Normal", "normal-value")
	_, err := client.CallUnary(context.Background(), req)
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	var clientSpan *mocktracer.Span
	for _, s := range spans {
		if s.Tag(ext.SpanKind) == ext.SpanKindClient {
			clientSpan = s
			break
		}
	}
	require.NotNil(t, clientSpan)
	assert.Nil(t, clientSpan.Tag(tagHeaderPrefix+"x-secret"))
	assert.Equal(t, "normal-value", clientSpan.Tag(tagHeaderPrefix+"x-normal"))
}

func TestStreamCallsDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewInterceptor(WithStreamCalls(false))
	mux := http.NewServeMux()
	mux.Handle("/test.v1.TestService/ServerStream", connect.NewServerStreamHandler(
		"/test.v1.TestService/ServerStream",
		func(_ context.Context, _ *connect.Request[testRequest], stream *connect.ServerStream[testResponse]) error {
			if err := stream.Send(&testResponse{Message: "msg1"}); err != nil {
				return err
			}
			return stream.Send(&testResponse{Message: "msg2"})
		},
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientInterceptor := NewInterceptor(WithStreamCalls(false))
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+"/test.v1.TestService/ServerStream",
		connect.WithInterceptors(clientInterceptor),
		jsonCodecOpt,
	)
	stream, err := client.CallServerStream(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)
	for stream.Receive() {
		_ = stream.Msg()
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	spans := mt.FinishedSpans()
	// With stream calls disabled, there should be no call-level spans.
	// There should only be message-level spans.
	for _, s := range spans {
		assert.Equal(t, "connect.message", s.OperationName())
	}
}

func TestStreamMessagesDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewInterceptor(WithStreamMessages(false))
	mux := http.NewServeMux()
	mux.Handle("/test.v1.TestService/ServerStream", connect.NewServerStreamHandler(
		"/test.v1.TestService/ServerStream",
		func(_ context.Context, _ *connect.Request[testRequest], stream *connect.ServerStream[testResponse]) error {
			if err := stream.Send(&testResponse{Message: "msg1"}); err != nil {
				return err
			}
			return stream.Send(&testResponse{Message: "msg2"})
		},
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientInterceptor := NewInterceptor(WithStreamMessages(false))
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+"/test.v1.TestService/ServerStream",
		connect.WithInterceptors(clientInterceptor),
		jsonCodecOpt,
	)
	stream, err := client.CallServerStream(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)
	for stream.Receive() {
		_ = stream.Msg()
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	spans := mt.FinishedSpans()
	// With stream messages disabled, no message-level spans should be created.
	for _, s := range spans {
		assert.NotEqual(t, "connect.message", s.OperationName())
	}
}

func TestStreaming(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewInterceptor()
	mux := http.NewServeMux()
	mux.Handle("/test.v1.TestService/ServerStream", connect.NewServerStreamHandler(
		"/test.v1.TestService/ServerStream",
		func(_ context.Context, _ *connect.Request[testRequest], stream *connect.ServerStream[testResponse]) error {
			if err := stream.Send(&testResponse{Message: "msg1"}); err != nil {
				return err
			}
			return stream.Send(&testResponse{Message: "msg2"})
		},
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientInterceptor := NewInterceptor()
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+"/test.v1.TestService/ServerStream",
		connect.WithInterceptors(clientInterceptor),
		jsonCodecOpt,
	)
	stream, err := client.CallServerStream(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)
	var msgs []string
	for stream.Receive() {
		msgs = append(msgs, stream.Msg().Message)
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	assert.Equal(t, []string{"msg1", "msg2"}, msgs)

	spans := mt.FinishedSpans()
	require.True(t, len(spans) > 2, "expected multiple spans for streaming, got %d", len(spans))

	var hasCallSpan, hasMessageSpan bool
	for _, s := range spans {
		if s.OperationName() == "connect.message" {
			hasMessageSpan = true
		} else {
			hasCallSpan = true
		}
		assert.Equal(t, componentName, s.Tag(ext.Component))
	}
	assert.True(t, hasCallSpan, "should have at least one call-level span")
	assert.True(t, hasMessageSpan, "should have at least one message-level span")
}

func TestBidiStreaming(t *testing.T) {
	// Bidi streaming requires HTTP/2. Use httptest.NewUnstartedServer with TLS.
	mt := mocktracer.Start()
	defer mt.Stop()

	interceptor := NewInterceptor()
	mux := http.NewServeMux()
	mux.Handle("/test.v1.TestService/BidiStream", connect.NewBidiStreamHandler(
		"/test.v1.TestService/BidiStream",
		func(_ context.Context, stream *connect.BidiStream[testRequest, testResponse]) error {
			for {
				req, err := stream.Receive()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := stream.Send(&testResponse{Message: "echo: " + req.Name}); err != nil {
					return err
				}
			}
		},
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	clientInterceptor := NewInterceptor()
	client := connect.NewClient[testRequest, testResponse](
		srv.Client(),
		srv.URL+"/test.v1.TestService/BidiStream",
		connect.WithInterceptors(clientInterceptor),
		jsonCodecOpt,
	)
	bidiStream := client.CallBidiStream(context.Background())

	err := bidiStream.Send(&testRequest{Name: "hello"})
	require.NoError(t, err)

	resp, err := bidiStream.Receive()
	require.NoError(t, err)
	assert.Equal(t, "echo: hello", resp.Message)

	require.NoError(t, bidiStream.CloseRequest())
	require.NoError(t, bidiStream.CloseResponse())

	spans := mt.FinishedSpans()
	require.True(t, len(spans) > 0, "expected spans for bidi streaming")

	// Verify there's at least one bidi stream span
	var hasBidiSpan bool
	for _, s := range spans {
		if s.Tag(tagMethodKind) == methodKindBidiStream {
			hasBidiSpan = true
			break
		}
	}
	assert.True(t, hasBidiSpan, "should have at least one bidi_streaming span")
}

func TestParseProcedure(t *testing.T) {
	tests := []struct {
		procedure string
		service   string
		method    string
	}{
		{"/test.v1.TestService/Ping", "test.v1.TestService", "Ping"},
		{"/com.example.FooService/Bar", "com.example.FooService", "Bar"},
		{"test.v1.TestService/Ping", "test.v1.TestService", "Ping"},
		{"/onlyservice", "onlyservice", ""},
	}

	for _, tt := range tests {
		service, method := parseProcedure(tt.procedure)
		assert.Equal(t, tt.service, service, "procedure: %s", tt.procedure)
		assert.Equal(t, tt.method, method, "procedure: %s", tt.procedure)
	}
}

func TestWithCustomTag(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := newTestServer(t, func(_ context.Context, _ *connect.Request[testRequest]) (*connect.Response[testResponse], error) {
		return connect.NewResponse(&testResponse{Message: "ok"}), nil
	}, WithCustomTag("custom.tag", "custom-value"))

	client := newTestClient(srv.URL, WithCustomTag("custom.tag", "custom-value"))
	_, err := client.CallUnary(context.Background(), connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	for _, s := range spans {
		assert.Equal(t, "custom-value", s.Tag("custom.tag"))
	}
}

func TestStreamingDistributedTracing(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Create a parent span to test context propagation
	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "test.parent")

	interceptor := NewInterceptor()
	mux := http.NewServeMux()
	mux.Handle("/test.v1.TestService/ServerStream", connect.NewServerStreamHandler(
		"/test.v1.TestService/ServerStream",
		func(_ context.Context, _ *connect.Request[testRequest], stream *connect.ServerStream[testResponse]) error {
			return stream.Send(&testResponse{Message: "ok"})
		},
		connect.WithInterceptors(interceptor),
		jsonCodecOpt,
	))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	clientInterceptor := NewInterceptor()
	client := connect.NewClient[testRequest, testResponse](
		http.DefaultClient,
		srv.URL+"/test.v1.TestService/ServerStream",
		connect.WithInterceptors(clientInterceptor),
		jsonCodecOpt,
	)
	stream, err := client.CallServerStream(ctx, connect.NewRequest(&testRequest{Name: "test"}))
	require.NoError(t, err)
	for stream.Receive() {
		_ = stream.Msg()
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())

	parentSpan.Finish()

	spans := mt.FinishedSpans()
	require.True(t, len(spans) > 1, "expected multiple spans")

	// All spans should share the same trace ID
	parentMock := mocktracer.MockSpan(parentSpan)
	traceID := parentMock.TraceID()
	for _, s := range spans {
		assert.Equal(t, traceID, s.TraceID(), "all spans should share the same trace ID")
	}
}
