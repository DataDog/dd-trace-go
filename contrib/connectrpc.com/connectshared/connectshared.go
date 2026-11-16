// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package connectshared holds the tracing core shared by the two dd-trace-go integrations for
// connectrpc.com/connect: github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect/v2, which
// traces connect v1, and github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect.v2/v2,
// which traces connect v2.
//
// It exists only for those integrations and is not meant to be used directly: use one of them
// instead. It imports neither major version of connectrpc.com/connect. Its API only grows,
// because older versions of those integrations are built against newer versions of this package.
package connectshared

import "strconv"

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

	// otherMethod is the rpc.method of an unrecognized method, see
	// https://opentelemetry.io/docs/specs/semconv/rpc/rpc-spans/.
	otherMethod = "_OTHER"

	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
	methodKindUnknown      = "unknown"
)

// Code is a connect error code. Both major versions of connect use the same values, so a
// connect Code converts to it by value.
type Code uint32

// The codes that the shared tracing logic relies on.
const (
	CodeCanceled         Code = 1
	CodeUnknown          Code = 2
	CodeDeadlineExceeded Code = 4
	CodeInternal         Code = 13
)

var codeNames = [...]string{
	1:  "canceled",
	2:  "unknown",
	3:  "invalid_argument",
	4:  "deadline_exceeded",
	5:  "not_found",
	6:  "already_exists",
	7:  "permission_denied",
	8:  "resource_exhausted",
	9:  "failed_precondition",
	10: "aborted",
	11: "out_of_range",
	12: "unimplemented",
	13: "internal",
	14: "unavailable",
	15: "data_loss",
	16: "unauthenticated",
}

// String returns the same name as connect's Code.String.
func (c Code) String() string {
	if c < Code(len(codeNames)) && codeNames[c] != "" {
		return codeNames[c]
	}
	return "code_" + strconv.FormatUint(uint64(c), 10)
}

// StreamType is a connect stream type. Both major versions of connect use the same values, so a
// connect StreamType converts to it by value.
type StreamType uint8

// The stream types of the four kinds of RPC.
const (
	StreamTypeUnary  StreamType = 0b00
	StreamTypeClient StreamType = 0b01
	StreamTypeServer StreamType = 0b10
	StreamTypeBidi              = StreamTypeClient | StreamTypeServer
)

// Spec is the part of a connect Spec that spans are tagged with.
type Spec struct {
	// Procedure is the RPC's procedure, such as "/acme.foo.v1.FooService/Bar".
	Procedure string
	// StreamType is the RPC's stream type, converted by value from connect's.
	StreamType StreamType
	// Unrecognized is set when the server serves Procedure without having registered it, as
	// connect.v2's unknown handler does: the client chose it. The spans then have the resource and
	// rpc.method "_OTHER", as OpenTelemetry names an unrecognized method, and the requested method
	// as rpc.method_original.
	Unrecognized bool
}
