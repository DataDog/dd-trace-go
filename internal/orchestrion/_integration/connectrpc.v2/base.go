// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// The harness leaves DD_SERVICE unset, so Connect spans get the tracer's default service: the test
// binary name.
const defaultService = "connectrpc.v2.test"

// rpc is a procedure and the tags that identify its spans.
type rpc struct {
	spec    connect.Spec
	service string // rpc.service
	method  string // rpc.method
	kind    string // connect.method.kind
}

var methodKinds = map[connect.StreamType]string{
	connect.StreamTypeUnary:  "unary",
	connect.StreamTypeClient: "client_streaming",
	connect.StreamTypeServer: "server_streaming",
	connect.StreamTypeBidi:   "bidi_streaming",
}

func newRPC(service, method string, streamType connect.StreamType) rpc {
	return rpc{
		spec:    connect.Spec{StreamType: streamType, Procedure: "/" + service + "/" + method},
		service: service,
		method:  method,
		kind:    methodKinds[streamType],
	}
}

func (r rpc) procedure() string { return r.spec.Procedure }

const echoServiceName = "example.echo.v1.EchoService"

// The procedures of the example service, which is written by hand in the shape of
// protoc-gen-connect-go v2 output.
var (
	unaryRPC        = newRPC(echoServiceName, "Echo", connect.StreamTypeUnary)
	clientStreamRPC = newRPC(echoServiceName, "Collect", connect.StreamTypeClient)
	serverStreamRPC = newRPC(echoServiceName, "Expand", connect.StreamTypeServer)
	bidiStreamRPC   = newRPC(echoServiceName, "Chat", connect.StreamTypeBidi)
)

// echoServiceClient has the shape of a generated service client.
type echoServiceClient struct {
	client *connect.Client
}

func newEchoServiceClient(client *connect.Client) *echoServiceClient {
	return &echoServiceClient{client: client}
}

func (c *echoServiceClient) Echo(ctx context.Context, req *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	var res wrapperspb.StringValue
	if err := c.client.CallUnary(ctx, unaryRPC.spec, req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *echoServiceClient) Collect(ctx context.Context) (collectClientStream, error) {
	stream, err := c.client.CallClientStream(ctx, clientStreamRPC.spec)
	if err != nil {
		return collectClientStream{}, err
	}
	return collectClientStream{stream: stream}, nil
}

func (c *echoServiceClient) Expand(ctx context.Context, req *wrapperspb.StringValue) (expandClientStream, error) {
	stream, err := c.client.CallServerStream(ctx, serverStreamRPC.spec, req)
	if err != nil {
		return expandClientStream{}, err
	}
	return expandClientStream{stream: stream}, nil
}

func (c *echoServiceClient) Chat(ctx context.Context) (chatClientStream, error) {
	stream, err := c.client.CallClientStream(ctx, bidiStreamRPC.spec)
	if err != nil {
		return chatClientStream{}, err
	}
	return chatClientStream{stream: stream}, nil
}

type collectClientStream struct {
	stream connect.ClientStream
}

func (s collectClientStream) Send(req *wrapperspb.StringValue) error {
	return s.stream.Send(req)
}

// CloseAndReceive reads the response without calling Close, like the generated method.
func (s collectClientStream) CloseAndReceive() (*wrapperspb.StringValue, error) {
	if err := s.stream.CloseSend(); err != nil {
		return nil, err
	}
	var res wrapperspb.StringValue
	if err := s.stream.Receive(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

type expandClientStream struct {
	stream connect.ClientStream
}

func (s expandClientStream) Receive() (*wrapperspb.StringValue, error) {
	var res wrapperspb.StringValue
	if err := s.stream.Receive(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s expandClientStream) Close() error {
	return s.stream.Close()
}

type chatClientStream struct {
	stream connect.ClientStream
}

func (s chatClientStream) Send(req *wrapperspb.StringValue) error {
	return s.stream.Send(req)
}

func (s chatClientStream) CloseSend() error {
	return s.stream.CloseSend()
}

func (s chatClientStream) Receive() (*wrapperspb.StringValue, error) {
	var res wrapperspb.StringValue
	if err := s.stream.Receive(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s chatClientStream) Close() error {
	return s.stream.Close()
}

// echoServiceHandler has the shape of a generated service interface.
type echoServiceHandler interface {
	Echo(context.Context, *wrapperspb.StringValue) (*wrapperspb.StringValue, error)
	Collect(context.Context, collectServerStream) (*wrapperspb.StringValue, error)
	Expand(context.Context, *wrapperspb.StringValue, expandServerStream) error
	Chat(context.Context, chatServerStream) error
}

// registerEchoServiceHandler has the shape of a generated RegisterXHandler function.
func registerEchoServiceHandler(server *connect.Server, svc echoServiceHandler) {
	adapter := echoServiceAdapter{svc: svc}
	server.Register(
		connect.Method{Spec: unaryRPC.spec, Handler: adapter.echo},
		connect.Method{Spec: clientStreamRPC.spec, Handler: adapter.collect},
		connect.Method{Spec: serverStreamRPC.spec, Handler: adapter.expand},
		connect.Method{Spec: bidiStreamRPC.spec, Handler: adapter.chat},
	)
}

type collectServerStream struct {
	stream connect.ServerStream
}

func (s collectServerStream) Receive() (*wrapperspb.StringValue, error) {
	var req wrapperspb.StringValue
	if err := s.stream.Receive(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

type expandServerStream struct {
	stream connect.ServerStream
}

func (s expandServerStream) Send(res *wrapperspb.StringValue) error {
	return s.stream.Send(res)
}

type chatServerStream struct {
	stream connect.ServerStream
}

func (s chatServerStream) Receive() (*wrapperspb.StringValue, error) {
	var req wrapperspb.StringValue
	if err := s.stream.Receive(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

func (s chatServerStream) Send(res *wrapperspb.StringValue) error {
	return s.stream.Send(res)
}

// echoServiceAdapter has the shape of the generated adapter from the service interface to
// connect.ServerFunc handlers.
type echoServiceAdapter struct {
	svc echoServiceHandler
}

func (h echoServiceAdapter) echo(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
	var req wrapperspb.StringValue
	if err := stream.Receive(&req); err != nil {
		return err
	}
	res, err := h.svc.Echo(ctx, &req)
	if err != nil {
		return err
	}
	return stream.Send(res)
}

func (h echoServiceAdapter) collect(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
	res, err := h.svc.Collect(ctx, collectServerStream{stream: stream})
	if err != nil {
		return err
	}
	return stream.Send(res)
}

func (h echoServiceAdapter) expand(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
	var req wrapperspb.StringValue
	if err := stream.Receive(&req); err != nil {
		return err
	}
	return h.svc.Expand(ctx, &req, expandServerStream{stream: stream})
}

func (h echoServiceAdapter) chat(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
	return h.svc.Chat(ctx, chatServerStream{stream: stream})
}

type echoService struct{}

func (echoService) Echo(_ context.Context, req *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return wrapperspb.String(req.GetValue()), nil
}

func (echoService) Collect(_ context.Context, stream collectServerStream) (*wrapperspb.StringValue, error) {
	var b strings.Builder
	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return wrapperspb.String(b.String()), nil
		}
		if err != nil {
			return nil, err
		}
		b.WriteString(req.GetValue())
	}
}

func (echoService) Expand(_ context.Context, req *wrapperspb.StringValue, stream expandServerStream) error {
	for _, suffix := range []string{"-1", "-2"} {
		if err := stream.Send(wrapperspb.String(req.GetValue() + suffix)); err != nil {
			return err
		}
	}
	return nil
}

func (echoService) Chat(_ context.Context, stream chatServerStream) error {
	for {
		req, err := stream.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := stream.Send(wrapperspb.String("echo: " + req.GetValue())); err != nil {
			return err
		}
	}
}

// callEcho calls the unary RPC and checks the response.
func callEcho(ctx context.Context, t *testing.T, client *echoServiceClient) {
	t.Helper()
	res, err := client.Echo(ctx, wrapperspb.String("hello"))
	require.NoError(t, err)
	require.Equal(t, "hello", res.GetValue())
}

// callChat calls the bidi RPC: Send and Receive twice, CloseSend, Receive to io.EOF, then Close.
func callChat(ctx context.Context, t *testing.T, client *echoServiceClient) {
	t.Helper()
	stream, err := client.Chat(ctx)
	require.NoError(t, err)
	defer func() { assert.NoError(t, stream.Close()) }()
	for _, msg := range []string{"ping", "pong"} {
		require.NoError(t, stream.Send(wrapperspb.String(msg)))
		res, err := stream.Receive()
		require.NoError(t, err)
		require.Equal(t, "echo: "+msg, res.GetValue())
	}
	require.NoError(t, stream.CloseSend())
	_, err = stream.Receive()
	require.ErrorIs(t, err, io.EOF)
}

func countServerCalls(n *atomic.Int32) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			n.Add(1)
			return next(ctx, spec, stream)
		}
	}
}

func countClientCalls(n *atomic.Int32) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			n.Add(1)
			return next(ctx, spec)
		}
	}
}

// server serves a connect.Server over HTTP/2 with TLS, which bidi streams require.
type server struct {
	srv *httptest.Server
}

func (s *server) start(t *testing.T, connectServer *connect.Server) {
	mux := http.NewServeMux()
	connecthttp.Mount(mux, connectServer)
	s.srv = httptest.NewUnstartedServer(mux)
	s.srv.EnableHTTP2 = true
	s.srv.StartTLS()
	t.Cleanup(s.srv.Close)
}

// transport returns a transport that sends RPCs to the server with the server's HTTP client.
func (s *server) transport(opts ...connecthttp.Option) connect.Transport {
	return connecthttp.NewTransport(s.srv.Client(), s.srv.URL, opts...)
}

func (s *server) url(r rpc) string { return s.srv.URL + r.procedure() }

// stop blocks until all outstanding requests complete, so the net/http server span, which finishes
// after the Connect handler returns, is finished before the harness stops the tracer.
func (s *server) stop() { s.srv.Close() }

func (s *server) addr() *net.TCPAddr { return s.srv.Listener.Addr().(*net.TCPAddr) }

func connectSpan(name string, r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     name,
			"service":  defaultService,
			"resource": r.procedure(),
			"type":     "rpc",
		},
		Meta: map[string]string{
			"component":           "connectrpc.com/connect.v2",
			"rpc.system":          "connectrpc",
			"rpc.service":         r.service,
			"rpc.method":          r.method,
			"connect.method.kind": r.kind,
		},
		Children: children,
	}
}

// messageSpans returns n connect.message spans. They have no span.kind because they are never
// trace roots here. Every test case implements ExpectedSpanCount, so the harness matches each
// expected span to a different span: the parent needs n of them, and the count rules out more.
func messageSpans(r rpc, n int) []*trace.Trace {
	spans := make([]*trace.Trace, 0, n)
	for range n {
		spans = append(spans, connectSpan("connect.message", r))
	}
	return spans
}

// inProcessClientCallSpan returns the connect.client span with the given number of client message
// spans, followed by children. It has no peer tags, as for the in-process transport.
func inProcessClientCallSpan(r rpc, messages int, children ...*trace.Trace) *trace.Trace {
	span := connectSpan("connect.client", r, append(messageSpans(r, messages), children...)...)
	span.Meta["span.kind"] = "client"
	return span
}

// serverCallSpan returns the connect.server span with the given number of server message spans.
func serverCallSpan(r rpc, messages int) *trace.Trace {
	span := connectSpan("connect.server", r, messageSpans(r, messages)...)
	span.Meta["span.kind"] = "server"
	return span
}

// asGRPC retags span, a call span, and its message spans for the gRPC and gRPC-Web protocols, with
// the given rpc.grpc.status_code on the call span.
func asGRPC(span *trace.Trace, statusCode float64) *trace.Trace {
	for _, s := range append([]*trace.Trace{span}, span.Children...) {
		if s != span && s.Tags["name"] != "connect.message" {
			continue
		}
		s.Meta["rpc.system"] = "grpc"
		s.Meta["rpc.grpc.full_method"] = s.Tags["resource"].(string)
	}
	setMetric(span, "rpc.grpc.status_code", statusCode)
	return span
}

func setMetric(span *trace.Trace, key string, value float64) {
	if span.Metrics == nil {
		span.Metrics = make(map[string]float64)
	}
	span.Metrics[key] = value
}

func (s *server) addPeerTags(span *trace.Trace) {
	span.Meta["network.destination.ip"] = s.addr().IP.String()
	setMetric(span, "network.destination.port", float64(s.addr().Port))
}

// clientCallSpan returns the connect.client span with the given number of client message spans,
// followed by children. It and its message spans have the server's peer tags.
func (s *server) clientCallSpan(r rpc, messages int, children ...*trace.Trace) *trace.Trace {
	span := inProcessClientCallSpan(r, messages, children...)
	s.addPeerTags(span)
	for _, msg := range span.Children[:messages] {
		s.addPeerTags(msg)
	}
	return span
}

// httpClientSpan returns the span of the woven http.Transport. Its service is inherited from the
// connect.client span, or is the default service when it is the trace root.
func (s *server) httpClientSpan(r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"service":  defaultService,
			"resource": "POST " + r.procedure(),
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

// httpServerSpan returns the span of the woven http.Server. Its route is the procedure, which
// connecthttp.Mount registers as a pattern.
func (s *server) httpServerSpan(r rpc, children ...*trace.Trace) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "http.request",
			"service":  "http.router",
			"resource": "POST " + r.procedure(),
			"type":     "web",
		},
		Meta: map[string]string{
			"component":        "net/http",
			"span.kind":        "server",
			"http.method":      "POST",
			"http.status_code": "200",
			"http.url":         s.url(r),
			"http.host":        s.addr().String(),
			"http.route":       r.procedure(),
		},
		Children: children,
	}
}

// rpcTrace returns the trace of an RPC over HTTP: connect.client, the HTTP client and server spans,
// then connect.server, with the given numbers of client and server message spans.
func (s *server) rpcTrace(r rpc, clientMessages, serverMessages int) *trace.Trace {
	return s.clientCallSpan(r, clientMessages,
		s.httpClientSpan(r,
			s.httpServerSpan(r,
				serverCallSpan(r, serverMessages),
			),
		),
	)
}

// sendRecorder is a connecthttp.HTTPClient that records the span active when the last request was
// sent.
type sendRecorder struct {
	client connecthttp.HTTPClient
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
