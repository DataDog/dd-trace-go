// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"
	"connectrpc.com/connect/v2/connectproto"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// The test service is written by hand in the shape of protoc-gen-connect-go v2 output. Its
// requests and responses are wrapperspb.StringValue messages.
const (
	testServiceName       = "test.connect.v2.TestService"
	unaryProcedure        = "/test.connect.v2.TestService/Unary"
	clientStreamProcedure = "/test.connect.v2.TestService/ClientStream"
	serverStreamProcedure = "/test.connect.v2.TestService/ServerStream"
	bidiProcedure         = "/test.connect.v2.TestService/Bidi"
)

var (
	// unarySpec has no side effects, so clients built with connecthttp.WithHTTPGet call it with GET.
	unarySpec = connectrpc.Spec{
		StreamType:       connectrpc.StreamTypeUnary,
		IdempotencyLevel: connectrpc.IdempotencyNoSideEffects,
		Procedure:        unaryProcedure,
	}
	clientStreamSpec = connectrpc.Spec{StreamType: connectrpc.StreamTypeClient, Procedure: clientStreamProcedure}
	serverStreamSpec = connectrpc.Spec{StreamType: connectrpc.StreamTypeServer, Procedure: serverStreamProcedure}
	bidiSpec         = connectrpc.Spec{StreamType: connectrpc.StreamTypeBidi, Procedure: bidiProcedure}
)

// The user-visible operation names, tag names and values that the tests check.
const (
	operationClient  = "connect.client"
	operationServer  = "connect.server"
	operationMessage = "connect.message"

	tagMethodKind            = "connect.method.kind"
	tagConnectErrorCode      = "rpc.connect_rpc.error_code"
	tagGRPCStatusCode        = "rpc.grpc.status_code"
	tagConnectRequest        = "connect.request"
	tagGRPCRequest           = "grpc.request"
	tagRequestMetadataPrefix = "rpc.request.metadata."
	tagRPCMethodOriginal     = "rpc.method_original"

	// otherMethod is the rpc.method and resource of the server spans of an unrecognized method.
	otherMethod = "_OTHER"

	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
)

// methodKind returns the connect.method.kind tag of an RPC of streamType.
func methodKind(streamType connectrpc.StreamType) string {
	switch streamType {
	case connectrpc.StreamTypeUnary:
		return methodKindUnary
	case connectrpc.StreamTypeClient:
		return methodKindClientStream
	case connectrpc.StreamTypeServer:
		return methodKindServerStream
	case connectrpc.StreamTypeBidi:
		return methodKindBidiStream
	default:
		return "unknown"
	}
}

var (
	connectProtocol = connectshared.ProtocolFor(connectrpc.ProtocolNameConnect)
	grpcProtocol    = connectshared.ProtocolFor(connectrpc.ProtocolNameGRPC)
)

// Request values that make the test service fail, see testHandlerError. Any other value succeeds.
const (
	valueNotFound      = "not-found"      // CodeNotFound
	valueError         = "error"          // CodeInternal
	valueErrorDetails  = "error-details"  // CodeInternal with a detail that WithErrorDetailTags tags as "seconds:1"
	valueCodedCanceled = "coded-canceled" // CodeInternal caused by context.Canceled
	valueEOF           = "eof"            // a plain io.EOF
	valuePlainError    = "plain-error"    // an error without a connect code
	valueRemoteError   = "remote-error"   // a remote CodeNotFound, as returned by a call that the handler made
	valueNotModified   = "not-modified"   // connecthttp.NewNotModifiedError()
	valuePanic         = "panic"          // the handler panics with valuePanic
	valueOSDeadline    = "os-deadline"    // an uncoded error that wraps os.ErrDeadlineExceeded
	// valueFailAfterOne makes the server-streaming handler fail with CodeDataLoss after one message.
	valueFailAfterOne = "fail-after-one"
)

// testHandlerError returns the error the test service fails with when it receives value, or
// panics for valuePanic.
func testHandlerError(value string) error {
	switch value {
	case valueNotFound:
		return connectrpc.NewError(connectrpc.CodeNotFound, "not found")
	case valueError:
		return connectrpc.NewError(connectrpc.CodeInternal, "internal error")
	case valueErrorDetails:
		return errorWithDetails(connectrpc.CodeInternal)
	case valueCodedCanceled:
		return connectrpc.NewError(connectrpc.CodeInternal, "canceled").WithCause(context.Canceled)
	case valueEOF:
		return io.EOF
	case valuePlainError:
		return errors.New("plain error")
	case valueRemoteError:
		return connectrpc.NewError(connectrpc.CodeNotFound, "upstream not found").WithRemote()
	case valueNotModified:
		return connecthttp.NewNotModifiedError()
	case valuePanic:
		panic(valuePanic)
	case valueOSDeadline:
		return fmt.Errorf("query: %w", os.ErrDeadlineExceeded)
	default:
		return nil
	}
}

// errorWithDetails returns an error with one detail, which WithErrorDetailTags tags as
// "seconds:1".
func errorWithDetails(code connectrpc.Code) *connectrpc.Error {
	detail, err := connectproto.NewErrorDetail(&durationpb.Duration{Seconds: 1})
	if err != nil {
		panic(err)
	}
	return connectrpc.NewError(code, "internal error").WithDetail(detail)
}

// registerTestService registers the test service on server, like a generated RegisterXHandler.
func registerTestService(server *connectrpc.Server) {
	server.Register(
		connectrpc.Method{Spec: unarySpec, Handler: handleUnary},
		connectrpc.Method{Spec: clientStreamSpec, Handler: handleClientStream},
		connectrpc.Method{Spec: serverStreamSpec, Handler: handleServerStream},
		connectrpc.Method{Spec: bidiSpec, Handler: handleBidi},
	)
}

// handleUnary echoes the request, driving the stream like a generated unary adapter.
func handleUnary(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	var request wrapperspb.StringValue
	if err := stream.Receive(&request); err != nil {
		return err
	}
	if err := testHandlerError(request.Value); err != nil {
		return err
	}
	return stream.Send(wrapperspb.String(request.Value))
}

// handleClientStream receives until the end of the stream and echoes the last request, like a
// generated client-streaming adapter that sends its service method's response.
func handleClientStream(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	var value string
	for {
		var request wrapperspb.StringValue
		err := stream.Receive(&request)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		value = request.Value
	}
	if err := testHandlerError(value); err != nil {
		return err
	}
	return stream.Send(wrapperspb.String(value))
}

// handleServerStream receives one request, like a generated server-streaming adapter, and sends
// "<value>-1" and "<value>-2".
func handleServerStream(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	var request wrapperspb.StringValue
	if err := stream.Receive(&request); err != nil {
		return err
	}
	if err := testHandlerError(request.Value); err != nil {
		return err
	}
	for _, value := range []string{request.Value + "-1", request.Value + "-2"} {
		if err := stream.Send(wrapperspb.String(value)); err != nil {
			return err
		}
		if request.Value == valueFailAfterOne {
			return connectrpc.NewError(connectrpc.CodeDataLoss, "data loss")
		}
	}
	return nil
}

// handleBidi echoes every request until the end of the stream.
func handleBidi(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	for {
		var request wrapperspb.StringValue
		err := stream.Receive(&request)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := testHandlerError(request.Value); err != nil {
			return err
		}
		if err := stream.Send(wrapperspb.String(request.Value)); err != nil {
			return err
		}
	}
}

// callUnary calls the unary procedure like a generated client method.
func callUnary(ctx context.Context, client *connectrpc.Client, value string) (*wrapperspb.StringValue, error) {
	var response wrapperspb.StringValue
	if err := client.CallUnary(ctx, unarySpec, wrapperspb.String(value), &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// callClientStream sends values on the client-streaming procedure, then calls the generated
// CloseAndReceive, which closes the send side and receives the response but never calls Close.
// An io.EOF from Send means the server ended the stream: CloseAndReceive then reports why.
func callClientStream(ctx context.Context, client *connectrpc.Client, values ...string) (*wrapperspb.StringValue, error) {
	stream, err := client.CallClientStream(ctx, clientStreamSpec)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if err := stream.Send(wrapperspb.String(value)); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	var response wrapperspb.StringValue
	if err := stream.Receive(&response); err != nil {
		return nil, err
	}
	return &response, nil
}

// callServerStream calls the server-streaming procedure like a generated client, receives until
// the end of the stream and closes it, as a caller would with a deferred Close. It returns the
// received values, and the error that ended the stream unless it is io.EOF.
func callServerStream(ctx context.Context, client *connectrpc.Client, value string) ([]string, error) {
	stream, err := client.CallServerStream(ctx, serverStreamSpec, wrapperspb.String(value))
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	var values []string
	for {
		var response wrapperspb.StringValue
		err := stream.Receive(&response)
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			return values, err
		}
		values = append(values, response.Value)
	}
}

// callBidi sends every value on the bidirectional procedure and receives its echo, then closes the
// send side, receives the end of the stream and closes the stream. It returns the echoed values and
// the first error.
func callBidi(ctx context.Context, client *connectrpc.Client, values ...string) ([]string, error) {
	stream, err := client.CallClientStream(ctx, bidiSpec)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	var echoed []string
	for _, value := range values {
		// After an io.EOF from Send, Receive reports why the server ended the stream.
		if err := stream.Send(wrapperspb.String(value)); err != nil && !errors.Is(err, io.EOF) {
			return echoed, err
		}
		var response wrapperspb.StringValue
		if err := stream.Receive(&response); err != nil {
			return echoed, err
		}
		echoed = append(echoed, response.Value)
	}
	if err := stream.CloseSend(); err != nil {
		return echoed, err
	}
	var response wrapperspb.StringValue
	if err := stream.Receive(&response); !errors.Is(err, io.EOF) {
		return echoed, fmt.Errorf("want the end of the stream, got %v", err)
	}
	return echoed, nil
}

// messageCounts are the message spans that each side of an RPC finishes.
type messageCounts struct{ client, server int }

// testRPC calls one type of RPC with a request value, like a generated client.
type testRPC struct {
	name, procedure, method, kind string
	// call returns the response values.
	call func(ctx context.Context, client *connectrpc.Client, value string) ([]string, error)
	// want is what a successful call with "hello" returns.
	want []string
	// ok counts the message spans of a successful call, and failed those of a call whose handler
	// fails after receiving its request. connect drains single-request and single-response
	// streams internally, so the server-streaming handler and the client-streaming client each see
	// a single Receive.
	ok, failed messageCounts
}

var (
	unaryRPC = testRPC{
		name: "unary", procedure: unaryProcedure, method: "Unary", kind: methodKindUnary,
		call: func(ctx context.Context, client *connectrpc.Client, value string) ([]string, error) {
			response, err := callUnary(ctx, client, value)
			if err != nil {
				return nil, err
			}
			return []string{response.Value}, nil
		},
		want: []string{"hello"},
	}
	// Client: Send and Receive. Server: Receive, the Receive of the end of the stream, and Send.
	clientStreamRPC = testRPC{
		name: "client", procedure: clientStreamProcedure, method: "ClientStream", kind: methodKindClientStream,
		call: func(ctx context.Context, client *connectrpc.Client, value string) ([]string, error) {
			response, err := callClientStream(ctx, client, value)
			if err != nil {
				return nil, err
			}
			return []string{response.Value}, nil
		},
		want:   []string{"hello"},
		ok:     messageCounts{client: 2, server: 3},
		failed: messageCounts{client: 2, server: 2},
	}
	// Client: Send, two Receives and the Receive of the end of the stream. Server: Receive and two
	// Sends.
	serverStreamRPC = testRPC{
		name: "server", procedure: serverStreamProcedure, method: "ServerStream", kind: methodKindServerStream,
		call:   callServerStream,
		want:   []string{"hello-1", "hello-2"},
		ok:     messageCounts{client: 4, server: 3},
		failed: messageCounts{client: 2, server: 1},
	}
	// Each side: Send, Receive, and the Receive of the end of the stream.
	bidiRPC = testRPC{
		name: "bidi", procedure: bidiProcedure, method: "Bidi", kind: methodKindBidiStream,
		call: func(ctx context.Context, client *connectrpc.Client, value string) ([]string, error) {
			return callBidi(ctx, client, value)
		},
		want:   []string{"hello"},
		ok:     messageCounts{client: 3, server: 3},
		failed: messageCounts{client: 2, server: 1},
	}

	testRPCs = []testRPC{unaryRPC, clientStreamRPC, serverStreamRPC, bidiRPC}
)

// testTransport is a way for a test client to reach a test server.
type testTransport struct {
	name string
	// httpOptions select the connecthttp wire protocol; inProcess selects connectinprocess.
	httpOptions []connecthttp.Option
	inProcess   bool
	// system is the expected rpc.system, and grpc whether the gRPC status tags are expected.
	system string
	grpc   bool
}

// hasPeer reports whether the client has a network peer, and so network.destination.* tags.
func (tt testTransport) hasPeer() bool {
	return !tt.inProcess
}

var (
	connectTransport   = testTransport{name: "connect", system: ext.RPCSystemConnectRPC}
	grpcTransport      = testTransport{name: "grpc", httpOptions: []connecthttp.Option{connecthttp.WithGRPC()}, system: ext.RPCSystemGRPC, grpc: true}
	grpcWebTransport   = testTransport{name: "grpcweb", httpOptions: []connecthttp.Option{connecthttp.WithGRPCWeb()}, system: ext.RPCSystemGRPC, grpc: true}
	inProcessTransport = testTransport{name: "inprocess", inProcess: true, system: ext.RPCSystemConnectRPC}

	// testTransports are the transports that end-to-end tests run over.
	testTransports = []testTransport{connectTransport, grpcTransport, grpcWebTransport, inProcessTransport}
)

// rigOptions configures newTestRig. The zero value builds an untraced server with the test
// service.
type rigOptions struct {
	serverInterceptors []connectrpc.ServerInterceptor
	// register registers the server's methods; nil registers the test service.
	register func(*connectrpc.Server)
	// mountOptions are passed to connecthttp.Mount, and transportOptions to connecthttp.NewTransport
	// after the protocol option of the test transport.
	mountOptions, transportOptions []connecthttp.Option
	// discardErrorLog discards the HTTP server's error log, where net/http reports the handlers'
	// panics.
	discardErrorLog bool
}

// testRig is a test server and a client transport that reaches it.
type testRig struct {
	server    *connectrpc.Server
	transport connectrpc.Transport
	// httpServer is nil for the in-process transport.
	httpServer *httptest.Server
}

func newTestRig(t *testing.T, tt testTransport, opts rigOptions) *testRig {
	t.Helper()
	server := connectrpc.NewServer(opts.serverInterceptors...)
	register := opts.register
	if register == nil {
		register = registerTestService
	}
	register(server)
	rig := &testRig{server: server}
	if tt.inProcess {
		rig.transport = connectinprocess.New(server)
		return rig
	}
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, opts.mountOptions...)
	rig.httpServer = newHTTP2TestServer(t, mux, opts.discardErrorLog)
	rig.transport = connecthttp.NewTransport(rig.httpServer.Client(), rig.httpServer.URL, slices.Concat(tt.httpOptions, opts.transportOptions)...)
	return rig
}

// client returns a client of the rig's server with interceptors.
func (r *testRig) client(interceptors ...connectrpc.ClientInterceptor) *connectrpc.Client {
	return connectrpc.NewClient(r.transport, interceptors...)
}

// newTracedRig returns a rig whose server is traced with serverOpts, and a client of it traced
// with clientOpts.
func newTracedRig(t *testing.T, tt testTransport, serverOpts, clientOpts []Option) (*testRig, *connectrpc.Client) {
	t.Helper()
	rig := newTestRig(t, tt, rigOptions{serverInterceptors: []connectrpc.ServerInterceptor{NewServerInterceptor(serverOpts...)}})
	return rig, rig.client(NewClientInterceptor(clientOpts...))
}

// newHTTP2TestServer serves handler over TLS with HTTP/2, which bidirectional streams require. The
// server's Client trusts it. The server is closed when the test ends.
func newHTTP2TestServer(t *testing.T, handler http.Handler, discardErrorLog bool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	if discardErrorLog {
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
	}
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			server.Close()
		}()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			// Close waits for every handler: a stuck one fails the test rather than hanging it.
			t.Error("the test server did not close")
		}
	})
	return server
}

// inMemoryHTTPClient serves requests with handler on the caller's goroutine, so the handler's
// context derives from the client's and no network I/O is involved. Use it for unary calls only:
// the response is only returned once the handler is done.
type inMemoryHTTPClient struct{ handler http.Handler }

func (c inMemoryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

// startMockTracer starts a mock tracer that is stopped when the test ends.
func startMockTracer(t *testing.T) mocktracer.Tracer {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	return mt
}

// fakePeerAddr is the peer address of the streams opened by fakeClientFunc for traceFakeClient.
const fakePeerAddr = "localhost:8080"

// fakeClientStream is a ClientStream whose methods can be replaced. By default, SendHeaders, Send,
// CloseSend and Close succeed, and Receive reports the end of the stream with a plain io.EOF, as
// connectinprocess does.
type fakeClientStream struct {
	sendHeaders, send, closeSend, receive, close func() error
}

func (s *fakeClientStream) SendHeaders() error { return callOr(s.sendHeaders, nil) }
func (s *fakeClientStream) Send(any) error     { return callOr(s.send, nil) }
func (s *fakeClientStream) CloseSend() error   { return callOr(s.closeSend, nil) }
func (s *fakeClientStream) Receive(any) error  { return callOr(s.receive, io.EOF) }
func (s *fakeClientStream) Close() error       { return callOr(s.close, nil) }

// fakeClientFunc returns a ClientFunc that opens stream like a transport: it fills in the
// CallInfo's spec, protocol and peer address.
func fakeClientFunc(stream connectrpc.ClientStream, protocol, peerAddr string) connectrpc.ClientFunc {
	return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
		if info, ok := connectrpc.CallInfoForClientContext(ctx); ok {
			info.Spec, info.Protocol, info.PeerAddr = spec, protocol, peerAddr
		}
		return stream, nil
	}
}

// fakeTransport is a Transport that opens streams with a ClientFunc such as fakeClientFunc, so that
// a fake stream can be driven through connect.Client, which attaches a CallInfo to the context and
// calls the stream methods as generated clients do.
type fakeTransport connectrpc.ClientFunc

func (t fakeTransport) NewClientStream(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
	return t(ctx, spec)
}

// traceFakeClient opens a stream for spec through a client interceptor configured with opts, over
// a transport that opens stream with the Connect protocol and the peer fakePeerAddr. Unlike
// connect.Client, it attaches no CallInfo to ctx: unless ctx has one, the interceptor attaches it.
func traceFakeClient(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ClientStream, opts ...Option) (connectrpc.ClientStream, error) {
	return NewClientInterceptor(opts...)(fakeClientFunc(stream, connectrpc.ProtocolNameConnect, fakePeerAddr))(ctx, spec)
}

// newTestClientStream wraps stream around span without the interceptor, so that a test can drive
// the stream's bookkeeping directly. The stream uses the Connect protocol and the peer
// fakePeerAddr.
func newTestClientStream(cfg *config, spec connectrpc.Spec, stream connectrpc.ClientStream, span *tracer.Span) *clientStream {
	return newClientStream(context.Background(), cfg, stream, span, spec, new(connectrpc.Header), connectProtocol, connectshared.NewPeerTags(fakePeerAddr))
}

// sendHello sends a message on stream, ignoring the error.
func sendHello(stream connectrpc.ClientStream) { _ = stream.Send(wrapperspb.String("hello")) }

// fakeServerStream is a ServerStream whose methods can be replaced. By default, they succeed.
type fakeServerStream struct {
	receive, sendHeaders, send func() error
}

func (s *fakeServerStream) Receive(any) error  { return callOr(s.receive, nil) }
func (s *fakeServerStream) SendHeaders() error { return callOr(s.sendHeaders, nil) }
func (s *fakeServerStream) Send(any) error     { return callOr(s.send, nil) }

// newServerCallInfo returns a server CallInfo with protocol and a peer address. Set the request
// metadata on its RequestHeader. It has no TransportInfo, so the call is never a GET: test the
// server's 304 path over connecthttp with connecthttp.WithHTTPGet.
func newServerCallInfo(protocol string) *connectrpc.CallInfo {
	return &connectrpc.CallInfo{PeerAddr: "127.0.0.1:1234", Protocol: protocol}
}

// serveFake serves a call to spec with handler on stream, through a server with interceptors, as a
// transport does: connect.Server.Call attaches info to the handler's context, or a new CallInfo if
// info is nil. To test a ServerFunc called without connect.Server.Call, and so without a CallInfo,
// call the interceptor directly.
func serveFake(ctx context.Context, spec connectrpc.Spec, info *connectrpc.CallInfo, stream connectrpc.ServerStream, handler connectrpc.ServerFunc, interceptors ...connectrpc.ServerInterceptor) error {
	server := connectrpc.NewServer(interceptors...)
	server.Register(connectrpc.Method{Spec: spec, Handler: handler})
	return server.Call(ctx, spec.Procedure, info, stream)
}

// traceFakeServer serves a call to spec with handler on stream through a server interceptor
// configured with opts, with a Connect server CallInfo.
func traceFakeServer(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream, handler connectrpc.ServerFunc, opts ...Option) error {
	return serveFake(ctx, spec, newServerCallInfo(connectrpc.ProtocolNameConnect), stream, handler, NewServerInterceptor(opts...))
}

func receiveMessage(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	return stream.Receive(&wrapperspb.StringValue{})
}

func sendMessage(_ context.Context, _ connectrpc.Spec, stream connectrpc.ServerStream) error {
	return stream.Send(wrapperspb.String("hello"))
}

// assertStatus checks the status tags of span: code, or success if code is 0.
func assertStatus(t *testing.T, tt testTransport, span *mocktracer.Span, code connectrpc.Code) {
	t.Helper()
	name := fmt.Sprint(span.OperationName(), "/", span.Tag(ext.ServiceName))
	if tt.grpc {
		assert.EqualValues(t, code, span.Tag(tagGRPCStatusCode), name)
		assert.Nil(t, span.Tag(tagConnectErrorCode), name)
		return
	}
	assert.Nil(t, span.Tag(tagGRPCStatusCode), name)
	if code == 0 {
		assert.Nil(t, span.Tag(tagConnectErrorCode), name)
	} else {
		assert.Equal(t, code.String(), span.Tag(tagConnectErrorCode), name)
	}
}

// startAllSpanKinds starts and finishes, on each side, a call span and a message span that
// is its child, through the span-start helpers the interceptors use.
func startAllSpanKinds(ctx context.Context, cfg *config) {
	for _, component := range []instrumentation.Component{instrumentation.ComponentClient, instrumentation.ComponentServer} {
		call, callCtx := cfg.core.StartCallSpan(ctx, component, sharedSpec(bidiSpec), grpcProtocol, nil)
		if component == instrumentation.ComponentClient {
			var messages connectshared.ClientMessages
			messages.Init(callCtx, cfg.core, sharedSpec(bidiSpec), grpcProtocol, connectshared.PeerTags{})
			messages.Start().Finish()
		} else {
			var messages connectshared.ServerMessages
			messages.Init(callCtx, cfg.core, sharedSpec(bidiSpec), grpcProtocol, tracer.TextMapCarrier{})
			span, _ := messages.Start()
			span.Finish()
		}
		call.Finish()
	}
}

// requireCallPair waits until exactly one client and one server call span have finished, and
// checks their method kind; the server's span may finish after the client's call has returned.
func requireCallPair(t *testing.T, mt mocktracer.Tracer, kind string) (client, server *mocktracer.Span) {
	t.Helper()
	require.Eventually(t, func() bool {
		spans := mt.FinishedSpans()
		return len(spansNamed(spans, operationClient)) == 1 && len(spansNamed(spans, operationServer)) == 1
	}, 5*time.Second, time.Millisecond)
	spans := mt.FinishedSpans()
	client, server = spansNamed(spans, operationClient)[0], spansNamed(spans, operationServer)[0]
	require.Equal(t, kind, client.Tag(tagMethodKind))
	require.Equal(t, kind, server.Tag(tagMethodKind))
	return client, server
}

// waitForSpans waits until at least n spans have finished and returns the finished spans.
func waitForSpans(t *testing.T, mt mocktracer.Tracer, n int) []*mocktracer.Span {
	t.Helper()
	require.Eventually(t, func() bool { return len(mt.FinishedSpans()) >= n }, 5*time.Second, time.Millisecond)
	return mt.FinishedSpans()
}

func spansNamed(spans []*mocktracer.Span, operation string) []*mocktracer.Span {
	var named []*mocktracer.Span
	for _, span := range spans {
		if span.OperationName() == operation {
			named = append(named, span)
		}
	}
	return named
}

// childrenOf returns the spans whose parent is parent.
func childrenOf(spans []*mocktracer.Span, parent *mocktracer.Span) []*mocktracer.Span {
	var children []*mocktracer.Span
	for _, span := range spans {
		if span.ParentID() == parent.SpanID() && span.TraceID() == parent.TraceID() {
			children = append(children, span)
		}
	}
	return children
}

// metadataTag returns the first value of the metadata tag for key. The tracer flattens the
// []string values into "<tag>.<index>" tags.
func metadataTag(span *mocktracer.Span, key string) any {
	return span.Tag(tagRequestMetadataPrefix + key + ".0")
}

// tagsWithPrefix returns the span's tags whose keys start with prefix.
func tagsWithPrefix(span *mocktracer.Span, prefix string) map[string]any {
	tags := make(map[string]any)
	for key, value := range span.Tags() {
		if strings.HasPrefix(key, prefix) {
			tags[key] = value
		}
	}
	return tags
}

// requireReturns fails the test if fn does not return in time, which would mean a deadlock.
func requireReturns(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", what)
	}
}

// callOr returns fn(), or err if fn is nil. The fake streams use it for their replaceable methods.
func callOr(fn func() error, err error) error {
	if fn != nil {
		return fn()
	}
	return err
}

func returns(err error) func() error { return func() error { return err } }

func panics(value any) func() error { return func() error { panic(value) } }

// panicWith panics with value, which may be nil.
func panicWith(value any) { panic(value) }

// callOutcome is how a call ended: by returning, with a panic, possibly with a nil value, or with
// runtime.Goexit.
type callOutcome struct {
	panicked bool
	value    any
	goexit   bool
}

// outcomeOf runs fn on its own goroutine and reports how it ended. It fails the test if fn does not
// end in time, which would mean a deadlock.
func outcomeOf(t *testing.T, fn func()) callOutcome {
	t.Helper()
	done := make(chan callOutcome, 1)
	go func() {
		var o callOutcome
		ended := false
		defer func() {
			if !ended {
				o = callOutcome{goexit: true}
			}
			done <- o
		}()
		func() {
			returned := false
			defer func() {
				if !returned {
					o.panicked, o.value = true, recover()
				}
			}()
			fn()
			returned = true
		}()
		// Only a return or a recovered panic gets here: runtime.Goexit does not.
		ended = true
	}()
	select {
	case o := <-done:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end")
		return callOutcome{}
	}
}

// recoversNilPanics reports whether recover returns nil for panic(nil), as it does with
// GODEBUG=panicnil=1.
func recoversNilPanics() (recoversNil bool) {
	defer func() { recoversNil = recover() == nil }()
	panicWith(nil)
	return false
}

// runsWithPanicNil reports whether the test runs with GODEBUG=panicnil=1. Otherwise it runs the
// test again in a child process with that setting, which is process-wide, and fails unless it
// passes there; the caller then returns.
func runsWithPanicNil(t *testing.T) bool {
	t.Helper()
	if recoversNilPanics() {
		return true
	}
	var pattern []string
	for name := range strings.SplitSeq(t.Name(), "/") {
		pattern = append(pattern, "^"+regexp.QuoteMeta(name)+"$")
	}
	args := []string{"-test.run=" + strings.Join(pattern, "/"), "-test.v", "-test.count=1"}
	for _, arg := range os.Args[1:] {
		// With go test -cover, the child's coverage joins the parent's.
		if strings.HasPrefix(arg, "-test.gocoverdir=") {
			args = append(args, arg)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GODEBUG="+strings.TrimPrefix(os.Getenv("GODEBUG")+",panicnil=1", ","))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "child process output:\n%s", out)
	assert.Contains(t, string(out), "--- PASS: "+t.Name()+" ", "child process output:\n%s", out)
	return false
}

// nilConnectErrorWrapper wraps a nil *connectrpc.Error without calling any of its methods.
type nilConnectErrorWrapper struct{}

func (nilConnectErrorWrapper) Error() string { return "wrapped nil connect error" }
func (nilConnectErrorWrapper) Unwrap() error { return (*connectrpc.Error)(nil) }

// derefError dereferences its receiver in every method, like a typed-nil custom error would.
type derefError struct{ cause error }

func (e *derefError) Error() string { return e.cause.Error() }
func (e *derefError) Unwrap() error { return e.cause }

// derefIsError dereferences its receiver only in its Is method.
type derefIsError struct{ target error }

func (*derefIsError) Error() string          { return "deref is" }
func (e *derefIsError) Is(target error) bool { return target == e.target }

// wrappedDerefError and wrappedDerefIsError have a typed-nil error in their chain, so walking the
// chain panics.
var (
	wrappedDerefError   = fmt.Errorf("op: %w", (*derefError)(nil))
	wrappedDerefIsError = fmt.Errorf("op: %w", (*derefIsError)(nil))
)

// unsafeErrors are the errors that a stream operation or a handler may return and whose methods
// panic.
var unsafeErrors = []struct {
	name string
	err  error
	// wantError is what the spans record, if not the substitute for an unsafe error.
	wantError string
}{
	{name: "typed nil connect error", err: (*connectrpc.Error)(nil)},
	{name: "typed nil custom error", err: (*derefError)(nil)},
	{name: "nil connect error in the chain", err: nilConnectErrorWrapper{}},
	{name: "wrapped typed nil connect error", err: fmt.Errorf("op: %w", (*connectrpc.Error)(nil))},
	{name: "wrapped typed nil custom error", err: wrappedDerefError},
	{name: "wrapped typed nil error whose Is method panics", err: wrappedDerefIsError},
	// The tracer calls Error while it holds the span's lock.
	{name: "error whose Error method panics", err: &derefError{}, wantError: "panics when formatted"},
}
