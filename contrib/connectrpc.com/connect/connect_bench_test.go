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
}

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
}
