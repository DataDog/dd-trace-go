// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

// Tags used for Connect RPC
const (
	tagMethodName   = "rpc.connect.procedure"
	tagMethodKind   = "rpc.method.kind"
	tagCode         = "rpc.connect.status_code"
	tagHeaderPrefix = "rpc.connect.header."
	tagRequest      = "rpc.connect.request"
)

const (
	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
)
