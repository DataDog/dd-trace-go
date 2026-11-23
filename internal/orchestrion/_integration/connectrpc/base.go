// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// The harness leaves DD_SERVICE unset, so Connect spans get the tracer's default service: the test
// binary name.
const defaultService = "connectrpc.test"

// rpc is a procedure of the example service.
type rpc struct {
	procedure string
	method    string
	kind      string // connect.method.kind
}

var (
	unaryRPC        = rpc{procedure: "/example.echo.v1.EchoService/Echo", method: "Echo", kind: "unary"}
	clientStreamRPC = rpc{procedure: "/example.echo.v1.EchoService/Collect", method: "Collect", kind: "client_streaming"}
	serverStreamRPC = rpc{procedure: "/example.echo.v1.EchoService/Expand", method: "Expand", kind: "server_streaming"}
	bidiStreamRPC   = rpc{procedure: "/example.echo.v1.EchoService/Chat", method: "Chat", kind: "bidi_streaming"}
)

type echoClient = connect.Client[wrapperspb.StringValue, wrapperspb.StringValue]

func echo(_ context.Context, req *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
	return connect.NewResponse(wrapperspb.String(req.Msg.GetValue())), nil
}

// callServerStream calls the server-streaming RPC and checks it gets both responses.
func callServerStream(ctx context.Context, t *testing.T, client *echoClient) {
	t.Helper()
	stream, err := client.CallServerStream(ctx, connect.NewRequest(wrapperspb.String("hello")))
	require.NoError(t, err)
	var got []string
	for stream.Receive() {
		got = append(got, stream.Msg().GetValue())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	require.Equal(t, []string{"hello-1", "hello-2"}, got)
}

// server serves a Connect handler over HTTP/2 with TLS, which bidi streams require.
type server struct {
	srv *httptest.Server
}

func (s *server) start(t *testing.T, handler http.Handler) {
	s.srv = httptest.NewUnstartedServer(handler)
	s.srv.EnableHTTP2 = true
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
}

func (s *server) url(r rpc) string { return s.srv.URL + r.procedure }

// stop blocks until all outstanding requests complete, so the net/http server span, which finishes
// after the Connect handler returns, is finished before the harness stops the tracer.
func (s *server) stop() { s.srv.Close() }

func (s *server) addr() *net.TCPAddr { return s.srv.Listener.Addr().(*net.TCPAddr) }

func connectSpan(name string, r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     name,
			"service":  defaultService,
			"resource": r.procedure,
			"type":     "rpc",
		},
		Meta: map[string]string{
			"component":           "connectrpc.com/connect",
			"rpc.system":          "connectrpc",
			"rpc.service":         "example.echo.v1.EchoService",
			"rpc.method":          r.method,
			"connect.method.kind": r.kind,
		},
		Children: children,
	}
}

// messageSpans returns n connect.message spans. They have no span.kind because they are never
// trace roots here. The harness returns the first span matching each expectation, so identical
// siblings all match the same span: n documents the expected count but cannot enforce it.
func messageSpans(r rpc, n int) []*trace.Trace {
	spans := make([]*trace.Trace, 0, n)
	for range n {
		spans = append(spans, connectSpan("connect.message", r))
	}
	return spans
}

func (s *server) addPeerTags(span *trace.Trace) {
	span.Meta["network.destination.ip"] = s.addr().IP.String()
	span.Metrics = map[string]float64{"network.destination.port": float64(s.addr().Port)}
}

// clientCallSpan returns the connect.client span with the given number of client message spans,
// followed by children.
func (s *server) clientCallSpan(r rpc, messages int, children ...*trace.Trace) *trace.Trace {
	msgs := messageSpans(r, messages)
	for _, msg := range msgs {
		s.addPeerTags(msg)
	}
	span := connectSpan("connect.client", r, append(msgs, children...)...)
	span.Meta["span.kind"] = "client"
	s.addPeerTags(span)
	return span
}

// serverCallSpan returns the connect.server span with the given number of server message spans.
func serverCallSpan(r rpc, messages int) *trace.Trace {
	span := connectSpan("connect.server", r, messageSpans(r, messages)...)
	span.Meta["span.kind"] = "server"
	return span
}

// httpClientSpan returns the span of the woven http.Transport. Its service is inherited from the
// connect.client span, or is the default service when it is the trace root.
func (s *server) httpClientSpan(r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"service":  defaultService,
			"resource": "POST " + r.procedure,
			"type":     "http",
		},
		Meta: map[string]string{
			"component":                "net/http",
			"span.kind":                "client",
			"http.method":              "POST",
			"http.status_code":         "200",
			"http.url":                 s.url(r),
			"network.destination.name": s.addr().IP.String(),
		},
		Metrics:  map[string]float64{"network.destination.port": float64(s.addr().Port)},
		Children: children,
	}
}

// httpServerSpan returns the span of the woven http.Server.
func (s *server) httpServerSpan(r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"service":  "http.router",
			"resource": "POST " + r.procedure,
			"type":     "web",
		},
		Meta: map[string]string{
			"component":        "net/http",
			"span.kind":        "server",
			"http.method":      "POST",
			"http.status_code": "200",
			"http.url":         s.url(r),
			"http.host":        s.addr().String(),
		},
		Children: children,
	}
}

// sendRecorder is a connect.HTTPClient that records the span active when the last request was sent.
type sendRecorder struct {
	client connect.HTTPClient
	last   atomic.Pointer[sentRequest]
}

type sentRequest struct {
	traced bool // a span was active
	root   bool // the active span was the root of its trace
}

func (r *sendRecorder) Do(req *http.Request) (*http.Response, error) {
	span, ok := tracer.SpanFromContext(req.Context())
	r.last.Store(&sentRequest{traced: ok, root: ok && span.Root() == span})
	return r.client.Do(req)
}
