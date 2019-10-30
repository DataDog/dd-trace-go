// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package grpc

// Tags used for gRPC
const (
	tagMethod     = "grpc.method"
	tagCode       = "grpc.code"
	tagMethodKind = "grpc.method.kind"
)

const (
	methodKindUnary           = "unary"
	methodKindClientStreaming = "client_streaming"
	methodKindServerStreaming = "server_streaming"
	methodKindBidiStreaming   = "bidi_streaming"
)
