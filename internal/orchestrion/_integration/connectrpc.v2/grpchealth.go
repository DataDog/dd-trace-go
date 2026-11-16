// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	"connectrpc.com/grpchealth/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

var (
	healthCheckRPC = newRPC("grpc.health.v1.Health", "Check", connect.StreamTypeUnary)
	healthWatchRPC = newRPC("grpc.health.v1.Health", "Watch", connect.StreamTypeServer)
)

// TestCaseGRPCHealth uses the grpchealth handlers and client, which build their streams through
// the woven server and clients: Check over HTTP with the gRPC protocol, and Watch through the
// in-process transport, ended by the cancellation of its context. Over HTTP, the handler of a
// canceled stream can outlive the server's Close, so its spans could finish after the test.
type TestCaseGRPCHealth struct {
	server
	httpClient, inProcessClient *grpchealth.Client
	// watching is closed when the Watch stream waits for its second response.
	watching chan struct{}
	// watchReturned is closed when the server's Watch handler returns.
	watchReturned chan struct{}
}

func (tc *TestCaseGRPCHealth) Setup(_ context.Context, t *testing.T) {
	tc.watching = make(chan struct{})
	tc.watchReturned = make(chan struct{})
	srv := connect.NewServer(notifyReturn(healthWatchRPC.procedure(), tc.watchReturned))
	grpchealth.Register(srv, grpchealth.NewStaticChecker(echoServiceName))
	tc.start(t, srv)
	tc.httpClient = grpchealth.NewClient(connect.NewClient(tc.transport(connecthttp.WithGRPC())))
	tc.inProcessClient = grpchealth.NewClient(connect.NewClient(&receiveNotifier{
		Transport: connectinprocess.New(srv),
		procedure: healthWatchRPC.procedure(),
		receiving: tc.watching,
	}))
}

func (tc *TestCaseGRPCHealth) Run(ctx context.Context, t *testing.T) {
	res, err := tc.httpClient.Check(ctx, &grpchealth.CheckRequest{Service: echoServiceName})
	require.NoError(t, err)
	require.Equal(t, grpchealth.StatusServing, res.Status)
	tc.stop()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := tc.inProcessClient.Watch(watchCtx, &grpchealth.CheckRequest{Service: echoServiceName})
	event := <-events
	require.NoError(t, event.Err)
	require.Equal(t, grpchealth.StatusServing, event.Response.Status)
	// Cancel only once the stream waits for the next response, so that this Receive is traced.
	waitFor(ctx, t, tc.watching, "Watch did not wait for its second response")
	cancel()
	for range events {
		// Watch closes the channel after it closed the stream.
	}
	waitFor(ctx, t, tc.watchReturned, "the Watch handler did not return")
}

func (*TestCaseGRPCHealth) ExpectedSpanCount() int { return 11 }

func (tc *TestCaseGRPCHealth) ExpectedTraces() trace.Traces {
	return trace.Traces{
		asGRPC(tc.clientCallSpan(healthCheckRPC, 0,
			tc.httpClientSpan(healthCheckRPC,
				tc.httpServerSpan(healthCheckRPC,
					asGRPC(serverCallSpan(healthCheckRPC, 0), 0),
				),
			),
		), 0),
		// Client: the request Send, the first response, and the Receive that the cancellation ends.
		// Server: the request Receive and the first response; the handler returns when the stream
		// is canceled. The client's status is not checked: its last Receive can report either the
		// cancellation or the end of the stream, once the handler has returned.
		inProcessClientCallSpan(healthWatchRPC, 3, serverCallSpan(healthWatchRPC, 2)),
	}
}

// notifyReturn returns an interceptor that closes returned when a call to procedure returns. As
// the outer interceptor, it returns after the woven one has finished the call's span.
func notifyReturn(procedure string, returned chan struct{}) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if spec.Procedure == procedure {
				defer close(returned)
			}
			return next(ctx, spec, stream)
		}
	}
}

func waitFor(ctx context.Context, t *testing.T, ch <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal(msg)
	}
}

// receiveNotifier is a connect.Transport that closes receiving when a stream of procedure calls
// Receive for the second time.
type receiveNotifier struct {
	connect.Transport
	procedure string
	receiving chan struct{}
}

func (n *receiveNotifier) NewClientStream(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
	stream, err := n.Transport.NewClientStream(ctx, spec)
	if err != nil || spec.Procedure != n.procedure {
		return stream, err
	}
	return &notifyingStream{ClientStream: stream, receiving: n.receiving}, nil
}

type notifyingStream struct {
	connect.ClientStream
	receives  int // Receive is never called concurrently with itself
	receiving chan struct{}
}

func (s *notifyingStream) Receive(msg any) error {
	s.receives++
	if s.receives == 2 {
		close(s.receiving)
	}
	return s.ClientStream.Receive(msg)
}
