// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package grpc

// Tags used for gRPC
const (
	tagMethodName = "grpc.method.name"
	tagMethodKind = "grpc.method.kind"
	tagCode       = "grpc.code"
)

const (
	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
)
