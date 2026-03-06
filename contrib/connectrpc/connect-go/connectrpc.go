// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package connect provides functions to trace the connectrpc.com/connect package.
package connect // import "github.com/DataDog/dd-trace-go/contrib/connectrpc/connect-go/v2"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const componentName = "connectrpc/connect-go"

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageConnectRPC)
}

// cache a constant option: saves one allocation per call
var spanTypeRPC = tracer.SpanType(ext.AppTypeRPC)

// finishWithError applies finish option and a tag with Connect status code, disregarding OK, EOF, Canceled and configured non-error codes.
func finishWithError(span *tracer.Span, err error, cfg *config) {
	// EOF and context.Canceled are expected in streaming RPCs (normal stream termination).
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		err = nil
	}
	code := connect.CodeOf(err)
	if err == nil || cfg.nonErrorCodes[code] {
		err = nil
	}
	// Unlike gRPC's status.Code(nil) which returns codes.OK,
	// connect.CodeOf(nil) returns CodeUnknown. Set "ok" explicitly for nil errors.
	if err == nil {
		span.SetTag(tagCode, "ok")
	} else {
		span.SetTag(tagCode, code.String())
	}

	var finishOptions []tracer.FinishOption
	if err != nil {
		if cfg.noDebugStack {
			finishOptions = []tracer.FinishOption{tracer.WithError(err), tracer.NoDebugStack()}
		} else {
			finishOptions = []tracer.FinishOption{tracer.WithError(err)}
		}
	}
	span.Finish(finishOptions...)
}

// parseProcedure splits a Connect RPC procedure (e.g. "/acme.foo.v1.FooService/Bar")
// into service and method components.
func parseProcedure(procedure string) (service, method string) {
	procedure = strings.TrimPrefix(procedure, "/")
	parts := strings.SplitN(procedure, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return procedure, ""
}

// methodKindFromStreamType converts a connect.StreamType to a method kind string.
func methodKindFromStreamType(st connect.StreamType) string {
	switch st {
	case connect.StreamTypeUnary:
		return methodKindUnary
	case connect.StreamTypeClient:
		return methodKindClientStream
	case connect.StreamTypeServer:
		return methodKindServerStream
	case connect.StreamTypeBidi:
		return methodKindBidiStream
	default:
		return fmt.Sprintf("unknown(%d)", st)
	}
}

// setHeaderTags adds Connect RPC header values as span tags.
func setHeaderTags(headers http.Header, cfg *config, span *tracer.Span) {
	if !cfg.withHeaderTags {
		return
	}
	for k, v := range headers {
		lk := strings.ToLower(k)
		if _, ok := cfg.ignoredHeaders[lk]; !ok {
			span.SetTag(tagHeaderPrefix+lk, strings.Join(v, ","))
		}
	}
}
