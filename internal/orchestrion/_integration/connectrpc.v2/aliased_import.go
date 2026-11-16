// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectrpc

import (
	"context"
	"sync/atomic"
	"testing"

	connectv2 "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type (
	serverInterceptorList []connectv2.ServerInterceptor
	clientInterceptorList []connectv2.ClientInterceptor
)

// TestCaseAliasedImport imports connect under another name, shadows the names of the packages that
// the woven code uses, and spreads nil and slices of named types, which the woven code must all
// compile with.
type TestCaseAliasedImport struct {
	namedClient, nilClient *echoServiceClient
	serverIntercepted      atomic.Int32
	clientIntercepted      atomic.Int32
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	connect, connecttrace, slices := "connect", "connecttrace", "slices"
	_, _, _ = connect, connecttrace, slices

	namedSrv := connectv2.NewServer(serverInterceptorList{countServerCalls(&tc.serverIntercepted)}...)
	registerEchoServiceHandler(namedSrv, echoService{})
	nilSrv := connectv2.NewServer(nil...)
	registerEchoServiceHandler(nilSrv, echoService{})

	tc.namedClient = newEchoServiceClient(connectv2.NewClient(connectinprocess.New(nilSrv), clientInterceptorList{countClientCalls(&tc.clientIntercepted)}...))
	tc.nilClient = newEchoServiceClient(connectv2.NewClient(connectinprocess.New(namedSrv), nil...))
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	callEcho(ctx, t, tc.namedClient)
	callEcho(ctx, t, tc.nilClient)
	require.Equal(t, int32(1), tc.serverIntercepted.Load(), "user server interceptor calls")
	require.Equal(t, int32(1), tc.clientIntercepted.Load(), "user client interceptor calls")
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int { return 4 }

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	// One trace per call.
	return trace.Traces{
		inProcessClientCallSpan(unaryRPC, 0, serverCallSpan(unaryRPC, 0)),
		inProcessClientCallSpan(unaryRPC, 0, serverCallSpan(unaryRPC, 0)),
	}
}
