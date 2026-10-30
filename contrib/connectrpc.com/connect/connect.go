// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"errors"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageConnectRPC)
}

// NewInterceptor returns an interceptor that traces both clients and handlers.
func NewInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(true, true, opts...)
}

// NewClientInterceptor returns an interceptor that traces clients.
func NewClientInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(true, false, opts...)
}

// NewServerInterceptor returns an interceptor that traces handlers.
func NewServerInterceptor(opts ...Option) connectrpc.Interceptor {
	return newInterceptor(false, true, opts...)
}

type interceptor struct {
	cfg         *config
	traceClient bool
	traceServer bool
}

func newInterceptor(traceClient, traceServer bool, opts ...Option) *interceptor {
	cfg := newConfig(opts...)
	instr.Logger().Debug("contrib/connectrpc.com/connect: configuring interceptor: %#v", cfg.core)
	return &interceptor{cfg: cfg, traceClient: traceClient, traceServer: traceServer}
}

func (i *interceptor) WrapUnary(next connectrpc.UnaryFunc) connectrpc.UnaryFunc {
	return func(ctx context.Context, request connectrpc.AnyRequest) (connectrpc.AnyResponse, error) {
		spec := request.Spec()
		if spec.IsClient {
			if !i.traceClient {
				return next(ctx, request)
			}
			ctx, traced := i.cfg.core.ClaimCall(ctx, instrumentation.ComponentClient, spec.Procedure, request)
			if !traced {
				return next(ctx, request)
			}
			return i.cfg.traceUnaryClient(ctx, spec, request, next)
		}
		if !i.traceServer {
			return next(ctx, request)
		}
		ctx, traced := i.cfg.core.ClaimCall(ctx, instrumentation.ComponentServer, spec.Procedure, request)
		if !traced {
			return next(ctx, request)
		}
		return i.cfg.traceUnaryHandler(ctx, spec, request, next)
	}
}

func (i *interceptor) WrapStreamingClient(next connectrpc.StreamingClientFunc) connectrpc.StreamingClientFunc {
	if !i.traceClient {
		return next
	}
	return func(ctx context.Context, spec connectrpc.Spec) connectrpc.StreamingClientConn {
		ctx, traced := i.cfg.core.ClaimCall(ctx, instrumentation.ComponentClient, spec.Procedure, nil)
		if !traced {
			conn := next(ctx, spec)
			// Streams have no per-call identity, so a stream to the same procedure opened through
			// another client within a traced call is passed through too: keep it in the trace.
			if i.cfg.core.PassedThroughTraced(ctx, instrumentation.ComponentClient, spec.Procedure, nil) {
				i.cfg.core.Inject(ctx, tracer.HTTPHeadersCarrier(conn.RequestHeader()))
			}
			return conn
		}
		return i.cfg.traceStreamingClient(ctx, spec, next)
	}
}

func (i *interceptor) WrapStreamingHandler(next connectrpc.StreamingHandlerFunc) connectrpc.StreamingHandlerFunc {
	if !i.traceServer {
		return next
	}
	return func(ctx context.Context, conn connectrpc.StreamingHandlerConn) error {
		spec := conn.Spec()
		ctx, traced := i.cfg.core.ClaimCall(ctx, instrumentation.ComponentServer, spec.Procedure, nil)
		if !traced {
			return next(ctx, conn)
		}
		return i.cfg.traceStreamingHandler(ctx, spec, conn, next)
	}
}

// sharedSpec returns the part of spec that spans are tagged with.
func sharedSpec(spec connectrpc.Spec) connectshared.Spec {
	return connectshared.Spec{Procedure: spec.Procedure, StreamType: connectshared.StreamType(spec.StreamType)}
}

var messageFinishMode = connectshared.FinishMode{AllowEOF: true}

// library describes connect's errors. Every method of a nil *connectrpc.Error dereferences its
// receiver, Code included. connect's servers send the code of the *connectrpc.Error that a handler
// returns, whichever call it comes from, so no error is remote.
var library = connectshared.Library{
	InspectError: func(err error) connectshared.ErrorInfo {
		connectErr, ok := errors.AsType[*connectrpc.Error](err)
		switch {
		case !ok:
			return connectshared.ErrorInfo{}
		case connectErr == nil:
			return connectshared.ErrorInfo{Coded: true, Nil: true}
		default:
			return connectshared.ErrorInfo{Coded: true, Code: connectshared.Code(connectErr.Code())}
		}
	},
	IsNotModified: connectrpc.IsNotModifiedError,
	ErrorDetails: func(err error, tags connectshared.ErrorDetailTags) {
		if connectErr, ok := errors.AsType[*connectrpc.Error](err); ok && connectErr != nil {
			connectshared.TagErrorDetails(tags, connectErr.Details(), (*connectrpc.ErrorDetail).Value)
		}
	},
}
