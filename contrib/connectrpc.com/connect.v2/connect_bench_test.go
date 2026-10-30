// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"net/http"
	"testing"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

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

	server := connectrpc.NewServer(NewServerInterceptor())
	registerTestService(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	// In-process, so that the benchmark measures the interceptors rather than socket I/O.
	transport := connecthttp.NewTransport(inMemoryHTTPClient{handler: mux}, "http://connect.bench")
	newClient := func(opts ...Option) *connectrpc.Client {
		return connectrpc.NewClient(transport, NewClientInterceptor(opts...))
	}
	ctx := context.Background()
	// requireCall fails the benchmark unless a call with value fails exactly when wantErr is set, so
	// that the benchmark measures the intended path.
	requireCall := func(b *testing.B, client *connectrpc.Client, value string, wantErr bool) {
		b.Helper()
		if _, err := callUnary(ctx, client, value); (err != nil) != wantErr {
			b.Fatalf("calling with %q: %v", value, err)
		}
	}

	b.Run("ok", func(b *testing.B) {
		client := newClient()
		requireCall(b, client, "hello", false)
		b.ReportAllocs()
		for b.Loop() {
			_, _ = callUnary(ctx, client, "hello")
		}
	})

	b.Run("ok_with_metadata_tags", func(b *testing.B) {
		client := newClient(WithMetadataTags())
		requireCall(b, client, "hello", false)
		b.ReportAllocs()
		for b.Loop() {
			// Simulate a realistic amount of header traffic: a couple of application
			// headers alongside the propagation headers Datadog tracing itself adds.
			callCtx, info := connectrpc.NewClientContext(ctx)
			info.RequestHeader().Set("User-Agent", "connect-go/2.0.0")
			info.RequestHeader().Set("X-Request-Id", "9219028207762307503")
			_, _ = callUnary(callCtx, client, "hello")
		}
	})

	b.Run("error", func(b *testing.B) {
		client := newClient()
		requireCall(b, client, valueError, true)
		b.ReportAllocs()
		for b.Loop() {
			_, _ = callUnary(ctx, client, valueError)
		}
	})

	b.Run("error_no_stack", func(b *testing.B) {
		client := newClient(NoDebugStack())
		requireCall(b, client, valueError, true)
		b.ReportAllocs()
		for b.Loop() {
			_, _ = callUnary(ctx, client, valueError)
		}
	})

	for _, bench := range []struct {
		name string
		// traced is set if both the server and the client are traced with opts.
		traced   bool
		opts     []Option
		protocol []connecthttp.Option
	}{
		{name: "ok_with_service", traced: true, opts: []Option{WithService(benchService)}},
		{name: "ok_grpc", traced: true, protocol: []connecthttp.Option{connecthttp.WithGRPC()}},
		{name: "ok_no_interceptor"},
	} {
		b.Run(bench.name, func(b *testing.B) {
			var serverInterceptors []connectrpc.ServerInterceptor
			var clientInterceptors []connectrpc.ClientInterceptor
			if bench.traced {
				serverInterceptors = []connectrpc.ServerInterceptor{NewServerInterceptor(bench.opts...)}
				clientInterceptors = []connectrpc.ClientInterceptor{NewClientInterceptor(bench.opts...)}
			}
			server := connectrpc.NewServer(serverInterceptors...)
			registerTestService(server)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			transport := connecthttp.NewTransport(inMemoryHTTPClient{handler: mux}, "http://connect.bench", bench.protocol...)
			client := connectrpc.NewClient(transport, clientInterceptors...)
			requireCall(b, client, "hello", false)
			b.ReportAllocs()
			for b.Loop() {
				_, _ = callUnary(ctx, client, "hello")
			}
		})
	}
}

// benchService is the service of the benchmarks that configure one.
const benchService = "bench-service"

func BenchmarkStreamingInterceptor(b *testing.B) {
	tracer.Start(tracer.WithLogger(testutils.DiscardLogger()),
		tracer.WithEnv("test"),
		tracer.WithServiceVersion("0.1.2"))
	defer tracer.Stop()

	const messages = 10
	ctx := context.Background()
	message := wrapperspb.String("hello")

	b.Run("handler", func(b *testing.B) {
		server := connectrpc.NewServer(NewServerInterceptor())
		server.Register(connectrpc.Method{Spec: bidiSpec, Handler: func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
			for range messages {
				if err := stream.Send(message); err != nil {
					return err
				}
			}
			return nil
		}})
		// connect.Server.Call is how transports call handlers, and the only way to attach a server
		// CallInfo, which the interceptor reads.
		info := newServerCallInfo(connectrpc.ProtocolNameConnect)
		stream := &fakeServerStream{}
		b.ReportAllocs()
		for b.Loop() {
			_ = server.Call(ctx, bidiProcedure, info, stream)
		}
	})

	b.Run("client", func(b *testing.B) {
		newStream := NewClientInterceptor()(fakeClientFunc(&fakeClientStream{}, connectrpc.ProtocolNameConnect, "127.0.0.1:1234"))
		b.ReportAllocs()
		for b.Loop() {
			stream, _ := newStream(ctx, bidiSpec)
			for range messages {
				_ = stream.Send(message)
			}
			_ = stream.CloseSend()
			_ = stream.Receive(&wrapperspb.StringValue{})
			_ = stream.Close()
		}
	})

	sendMessages := func(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
		for range messages {
			if err := stream.Send(message); err != nil {
				return err
			}
		}
		return nil
	}
	// The request headers of a traced client propagate a trace context.
	propagated := tracer.StartSpan("propagated")
	propagated.Finish()
	carrier := tracer.TextMapCarrier{}
	if err := tracer.Inject(propagated.Context(), carrier); err != nil {
		b.Fatal(err)
	}
	tracedInfo := newServerCallInfo(connectrpc.ProtocolNameConnect)
	for key, value := range carrier {
		tracedInfo.RequestHeader().Set(key, value)
	}
	for _, bench := range []struct {
		name string
		// traced is set if the server is traced with opts.
		traced bool
		opts   []Option
		info   *connectrpc.CallInfo
	}{
		{name: "handler_with_service", traced: true, opts: []Option{WithService(benchService)}, info: newServerCallInfo(connectrpc.ProtocolNameConnect)},
		{name: "handler_grpc", traced: true, info: newServerCallInfo(connectrpc.ProtocolNameGRPC)},
		{name: "handler_without_stream_calls", traced: true, opts: []Option{WithStreamCalls(false)}, info: newServerCallInfo(connectrpc.ProtocolNameConnect)},
		{name: "handler_without_stream_calls_traced_headers", traced: true, opts: []Option{WithStreamCalls(false)}, info: tracedInfo},
		{name: "handler_no_interceptor", info: newServerCallInfo(connectrpc.ProtocolNameConnect)},
	} {
		b.Run(bench.name, func(b *testing.B) {
			var interceptors []connectrpc.ServerInterceptor
			if bench.traced {
				interceptors = []connectrpc.ServerInterceptor{NewServerInterceptor(bench.opts...)}
			}
			server := connectrpc.NewServer(interceptors...)
			server.Register(connectrpc.Method{Spec: bidiSpec, Handler: sendMessages})
			stream := &fakeServerStream{}
			b.ReportAllocs()
			for b.Loop() {
				_ = server.Call(ctx, bidiProcedure, bench.info, stream)
			}
		})
	}

	for _, bench := range []struct {
		name string
		// traced is set if the client is traced with opts.
		traced   bool
		opts     []Option
		protocol string
	}{
		{name: "client_with_service", traced: true, opts: []Option{WithService(benchService)}, protocol: connectrpc.ProtocolNameConnect},
		{name: "client_grpc", traced: true, protocol: connectrpc.ProtocolNameGRPC},
		{name: "client_no_interceptor", protocol: connectrpc.ProtocolNameConnect},
	} {
		b.Run(bench.name, func(b *testing.B) {
			newStream := fakeClientFunc(&fakeClientStream{}, bench.protocol, "127.0.0.1:1234")
			if bench.traced {
				newStream = NewClientInterceptor(bench.opts...)(newStream)
			}
			b.ReportAllocs()
			for b.Loop() {
				stream, _ := newStream(ctx, bidiSpec)
				for range messages {
					_ = stream.Send(message)
				}
				_ = stream.CloseSend()
				_ = stream.Receive(&wrapperspb.StringValue{})
				_ = stream.Close()
			}
		})
	}
}
