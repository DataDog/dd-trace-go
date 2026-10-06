// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/grpc/v2/fixturepb"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// connTrackerListener records every connection the gRPC server accepts, so a
// test can close the first one to simulate a transport failure.
type connTrackerListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *connTrackerListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, c)
	l.mu.Unlock()
	return c, nil
}

func (l *connTrackerListener) closeFirstConn() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.conns) > 0 {
		l.conns[0].Close()
		l.conns = l.conns[1:]
	}
}

// gatingFixtureServer makes the first StreamPing stream block until it is
// released, so it holds the single per-transport stream slot the rig allows.
// Any later stream then blocks its creation on the client transport while the
// first stream is active.
type gatingFixtureServer struct {
	fixturepb.UnimplementedFixtureServer
	mu        sync.Mutex
	calls     int
	release   chan struct{}
	firstRecv chan struct{}
	once      sync.Once
}

func newGatingFixtureServer() *gatingFixtureServer {
	return &gatingFixtureServer{
		release:   make(chan struct{}),
		firstRecv: make(chan struct{}),
	}
}

func (s *gatingFixtureServer) StreamPing(stream fixturepb.Fixture_StreamPingServer) error {
	_, err := stream.Recv()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()
	if call == 1 {
		s.once.Do(func() { close(s.firstRecv) })
		select {
		case <-s.release:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	return stream.Send(&fixturepb.FixtureReply{Message: "passed"})
}

// retryRig is a gRPC rig for the stream retry tests. The server allows a
// single concurrent stream and every accepted connection is tracked.
type retryRig struct {
	server   *gatingFixtureServer
	listener *connTrackerListener
	conn     *grpc.ClientConn
	client   fixturepb.FixtureClient
}

func newRetryRig(t *testing.T, clientOpts []grpc.DialOption) *retryRig {
	t.Helper()

	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := &connTrackerListener{Listener: li}
	server := grpc.NewServer(grpc.MaxConcurrentStreams(1))
	gating := newGatingFixtureServer()
	fixturepb.RegisterFixtureServer(server, gating)
	go server.Serve(listener)

	conn, err := grpc.Dial("localhost:"+strconv.Itoa(li.Addr().(*net.TCPAddr).Port), append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, clientOpts...)...)
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, conn.Close())
		server.Stop()
		listener.Close()
	})
	return &retryRig{
		server:   gating,
		listener: listener,
		conn:     conn,
		client:   fixturepb.NewFixtureClient(conn),
	}
}

// holdQuota opens a stream that holds the single per-transport stream slot
// until the rig server is released. It returns once the server received the
// first message, so the slot is guaranteed to be taken.
func (r *retryRig) holdQuota(t *testing.T) (release func(), cancel context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := r.client.StreamPing(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "hold"}))
	select {
	case <-r.server.firstRecv:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the server never received the first message")
	}
	return func() { close(r.server.release) }, cancel
}

// TestStreamRetries checks that the client stream wrapper respects the gRPC
// transparent retry contract (issue #4757) while tracing stream messages. The
// caller closes the retry window by calling Context, Header or RecvMsg, which
// commit the stream attempt in gRPC and disable transparent retries. The
// wrapper must record those commits the same way, whether call tracing is
// enabled or not.
func TestStreamRetries(t *testing.T) {
	clientOpts := []grpc.DialOption{
		grpc.WithStreamInterceptor(StreamClientInterceptor(WithStreamCalls(false))),
	}

	// killWhileQuotaHeld kills the client transport while a second stream is
	// blocked on the stream quota. gRPC treats a stream that never reached the
	// server as unprocessed and replays it on a new attempt, so the RPC
	// succeeds through a transparent retry.
	killWhileQuotaHeld := func(t *testing.T, rig *retryRig) fixturepb.Fixture_StreamPingClient {
		release, cancel := rig.holdQuota(t)
		defer cancel()

		// The second stream blocks its creation on the quota, so it never
		// reaches the server and gRPC may replay it.
		type streamRes struct {
			stream fixturepb.Fixture_StreamPingClient
			err    error
		}
		res := make(chan streamRes, 1)
		go func() {
			stream, err := rig.client.StreamPing(context.Background())
			res <- streamRes{stream, err}
		}()
		// Give the second stream time to block on the quota. Either way it
		// must succeed: it is replayed on a new attempt if it was already
		// waiting, or created on the reconnected transport otherwise.
		time.Sleep(200 * time.Millisecond)

		rig.listener.closeFirstConn()
		release()

		select {
		case r := <-res:
			require.NoError(t, r.err)
			return r.stream
		case <-time.After(10 * time.Second):
			t.Fatal("the stream was not created after the connection loss")
			return nil
		}
	}

	t.Run("no client interceptor", func(t *testing.T) {
		rig := newRetryRig(t, nil)

		stream := killWhileQuotaHeld(t, rig)
		require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "pass"}))
		reply, err := stream.Recv()
		require.NoError(t, err, "the RPC must survive the transport failure through a transparent retry")
		assert.Equal(t, "passed", reply.GetMessage())
	})

	t.Run("traced client stream", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig := newRetryRig(t, clientOpts)

		stream := killWhileQuotaHeld(t, rig)
		// The caller did not close the retry window, so the send must not read
		// the transport context, even though the first attempt already failed.
		require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "pass"}))
		reply, err := stream.Recv()
		require.NoError(t, err, "the RPC must survive the transport failure through a transparent retry")
		assert.Equal(t, "passed", reply.GetMessage())

		// The spans finish in order: hold-stream send, retried send, retried
		// receive. The send spans carry no peer tags because the retry window
		// was still open, while the receive span does because RecvMsg closed it.
		var msgSpans []*mocktracer.Span
		for _, s := range mt.FinishedSpans() {
			if s.OperationName() == "grpc.message" {
				msgSpans = append(msgSpans, s)
			}
		}
		require.Len(t, msgSpans, 3)
		for _, s := range msgSpans[:2] {
			assert.Nil(t, s.Tag(ext.TargetHost), "the send must not read the transport context while the retry window is open")
		}
		assert.Equal(t, "127.0.0.1", msgSpans[2].Tag(ext.TargetHost), "the receive must read the transport context after the window closed")
	})

	// Calling Context before Header or RecvMsg commits the stream attempt,
	// which disables transparent retries. The transport failure must then
	// surface to the caller instead of being replayed, exactly like with an
	// untraced stream.
	t.Run("caller commits through Context", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rig := newRetryRig(t, clientOpts)

		stream, err := rig.client.StreamPing(context.Background())
		require.NoError(t, err)
		// The commit happens in gRPC itself, but the wrapper records it so
		// that message spans know the retry window is closed.
		_ = stream.Context()
		require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "pass"}))
		rig.listener.closeFirstConn()

		_, err = stream.Recv()
		require.Error(t, err, "the RPC must fail once the caller committed the attempt")
		assert.Equal(t, codes.Unavailable, status.Code(err))

		// The send span carries the peer tags because Context already closed
		// the retry window when it was written.
		var sendSpan *mocktracer.Span
		for _, s := range mt.FinishedSpans() {
			if s.OperationName() == "grpc.message" {
				sendSpan = s
				break
			}
		}
		require.NotNil(t, sendSpan)
		assert.Equal(t, "127.0.0.1", sendSpan.Tag(ext.TargetHost), "the send must read the transport context after Context closed the retry window")
	})

	t.Run("no client interceptor commits through Context", func(t *testing.T) {
		rig := newRetryRig(t, nil)

		stream, err := rig.client.StreamPing(context.Background())
		require.NoError(t, err)
		_ = stream.Context()
		require.NoError(t, stream.Send(&fixturepb.FixtureRequest{Name: "pass"}))
		rig.listener.closeFirstConn()

		_, err = stream.Recv()
		require.Error(t, err, "the RPC must fail once the caller committed the attempt")
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})
}
