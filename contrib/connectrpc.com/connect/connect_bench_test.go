// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"io"
	"net/http"
	"testing"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

func BenchmarkUnaryInterceptor(b *testing.B) {
	// need to use the real tracer to get representative measurements
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	handler := connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler,
		connectrpc.WithInterceptors(NewServerInterceptor()))
	// In-process, so that the benchmark measures the interceptors rather than socket I/O.
	httpClient := inMemoryHTTPClient{handler: handler}

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

	b.Run("ok_with_metadata_tags", func(b *testing.B) {
		client := newClient(WithMetadataTags())
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

	for _, bench := range []struct {
		name string
		// traced is set if both the handler and the client are traced with opts.
		traced   bool
		opts     []Option
		protocol []connectrpc.ClientOption
	}{
		{name: "ok_with_service", traced: true, opts: []Option{WithService(benchService)}},
		{name: "ok_grpc", traced: true, protocol: []connectrpc.ClientOption{connectrpc.WithGRPC()}},
		{name: "ok_no_interceptor"},
	} {
		b.Run(bench.name, func(b *testing.B) {
			var handlerOpts []connectrpc.HandlerOption
			clientOpts := bench.protocol
			if bench.traced {
				handlerOpts = []connectrpc.HandlerOption{connectrpc.WithInterceptors(NewServerInterceptor(bench.opts...))}
				clientOpts = append(clientOpts, connectrpc.WithInterceptors(NewClientInterceptor(bench.opts...)))
			}
			handler := connectrpc.NewUnaryHandler(unaryProcedure, unaryHandler, handlerOpts...)
			client := connectrpc.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](
				inMemoryHTTPClient{handler: handler}, "http://connect.bench"+unaryProcedure, clientOpts...)
			if _, err := client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("hello"))); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = client.CallUnary(ctx, connectrpc.NewRequest(wrapperspb.String("hello")))
			}
		})
	}
}

// benchService is the service of the benchmarks that configure one.
const benchService = "bench-service"

type benchHandlerConn struct{ header http.Header }

func (benchHandlerConn) Spec() connectrpc.Spec {
	return connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi}
}

func (benchHandlerConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "127.0.0.1:1234", Protocol: connectrpc.ProtocolConnect}
}
func (benchHandlerConn) Receive(any) error            { return nil }
func (c benchHandlerConn) RequestHeader() http.Header { return c.header }
func (benchHandlerConn) Send(any) error               { return nil }
func (benchHandlerConn) ResponseHeader() http.Header  { return nil }
func (benchHandlerConn) ResponseTrailer() http.Header { return nil }

// benchGRPCHandlerConn is a benchHandlerConn over gRPC.
type benchGRPCHandlerConn struct{ benchHandlerConn }

func (benchGRPCHandlerConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "127.0.0.1:1234", Protocol: connectrpc.ProtocolGRPC}
}

type benchClientConn struct{ header http.Header }

func (benchClientConn) Spec() connectrpc.Spec {
	return connectrpc.Spec{Procedure: bidiProcedure, StreamType: connectrpc.StreamTypeBidi, IsClient: true}
}

func (benchClientConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "127.0.0.1:1234", Protocol: connectrpc.ProtocolConnect}
}
func (benchClientConn) Send(any) error               { return nil }
func (c benchClientConn) RequestHeader() http.Header { return c.header }
func (benchClientConn) CloseRequest() error          { return nil }
func (benchClientConn) Receive(any) error            { return io.EOF }
func (benchClientConn) ResponseHeader() http.Header  { return nil }
func (benchClientConn) ResponseTrailer() http.Header { return nil }
func (benchClientConn) CloseResponse() error         { return nil }

// benchGRPCClientConn is a benchClientConn over gRPC.
type benchGRPCClientConn struct{ benchClientConn }

func (benchGRPCClientConn) Peer() connectrpc.Peer {
	return connectrpc.Peer{Addr: "127.0.0.1:1234", Protocol: connectrpc.ProtocolGRPC}
}

func BenchmarkStreamingInterceptor(b *testing.B) {
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	const messages = 10
	ctx := context.Background()
	message := wrapperspb.String("hello")
	interceptor := NewInterceptor()

	b.Run("handler", func(b *testing.B) {
		handler := interceptor.WrapStreamingHandler(func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
			for range messages {
				if err := conn.Send(message); err != nil {
					return err
				}
			}
			return nil
		})
		conn := benchHandlerConn{header: make(http.Header)}
		b.ReportAllocs()
		for b.Loop() {
			_ = handler(ctx, conn)
		}
	})

	b.Run("client", func(b *testing.B) {
		newConn := interceptor.WrapStreamingClient(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
			return benchClientConn{header: make(http.Header)}
		})
		spec := benchClientConn{}.Spec()
		b.ReportAllocs()
		for b.Loop() {
			conn := newConn(ctx, spec)
			for range messages {
				_ = conn.Send(message)
			}
			_ = conn.CloseRequest()
			_ = conn.Receive(&wrapperspb.StringValue{})
			_ = conn.CloseResponse()
		}
	})

	sendMessages := func(_ context.Context, conn connectrpc.StreamingHandlerConn) error {
		for range messages {
			if err := conn.Send(message); err != nil {
				return err
			}
		}
		return nil
	}
	// The request headers of a traced client propagate a trace context.
	propagated := tracer.StartSpan("propagated")
	propagated.Finish()
	tracedHeader := make(http.Header)
	if err := tracer.Inject(propagated.Context(), tracer.HTTPHeadersCarrier(tracedHeader)); err != nil {
		b.Fatal(err)
	}
	for _, bench := range []struct {
		name string
		// traced is set if the handler is traced with opts.
		traced bool
		opts   []Option
		conn   connectrpc.StreamingHandlerConn
	}{
		{name: "handler_with_service", traced: true, opts: []Option{WithService(benchService)}, conn: benchHandlerConn{header: make(http.Header)}},
		{name: "handler_grpc", traced: true, conn: benchGRPCHandlerConn{benchHandlerConn{header: make(http.Header)}}},
		{name: "handler_without_stream_calls", traced: true, opts: []Option{WithStreamCalls(false)}, conn: benchHandlerConn{header: make(http.Header)}},
		{name: "handler_without_stream_calls_traced_headers", traced: true, opts: []Option{WithStreamCalls(false)}, conn: benchHandlerConn{header: tracedHeader}},
		{name: "handler_no_interceptor", conn: benchHandlerConn{header: make(http.Header)}},
	} {
		b.Run(bench.name, func(b *testing.B) {
			handler := sendMessages
			if bench.traced {
				handler = NewInterceptor(bench.opts...).WrapStreamingHandler(sendMessages)
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = handler(ctx, bench.conn)
			}
		})
	}

	for _, bench := range []struct {
		name string
		// traced is set if the client is traced with opts.
		traced bool
		opts   []Option
		grpc   bool
	}{
		{name: "client_with_service", traced: true, opts: []Option{WithService(benchService)}},
		{name: "client_grpc", traced: true, grpc: true},
		{name: "client_no_interceptor"},
	} {
		b.Run(bench.name, func(b *testing.B) {
			newConn := connectrpc.StreamingClientFunc(func(context.Context, connectrpc.Spec) connectrpc.StreamingClientConn {
				if bench.grpc {
					return benchGRPCClientConn{benchClientConn{header: make(http.Header)}}
				}
				return benchClientConn{header: make(http.Header)}
			})
			if bench.traced {
				newConn = NewInterceptor(bench.opts...).WrapStreamingClient(newConn)
			}
			spec := benchClientConn{}.Spec()
			b.ReportAllocs()
			for b.Loop() {
				conn := newConn(ctx, spec)
				for range messages {
					_ = conn.Send(message)
				}
				_ = conn.CloseRequest()
				_ = conn.Receive(&wrapperspb.StringValue{})
				_ = conn.CloseResponse()
			}
		})
	}
}
