// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/examples/helloworld/helloworld"
)

// TestCaseDial checks that a client created with the deprecated grpc.Dial is
// traced. It shares the server and expected traces of TestCase.
type TestCaseDial struct {
	TestCase
}

func (tc *TestCaseDial) Run(ctx context.Context, t *testing.T) {
	//nolint:staticcheck // grpc.Dial is deprecated, but still instrumented.
	conn, err := grpc.Dial(tc.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	sayHello(ctx, t, conn)
}

// TestCaseDialContext checks that a client created with the deprecated
// grpc.DialContext is traced. It shares the server and expected traces of
// TestCase.
type TestCaseDialContext struct {
	TestCase
}

func (tc *TestCaseDialContext) Run(ctx context.Context, t *testing.T) {
	//nolint:staticcheck // grpc.DialContext is deprecated, but still instrumented.
	conn, err := grpc.DialContext(ctx, tc.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	sayHello(ctx, t, conn)
}

func sayHello(ctx context.Context, t *testing.T, conn *grpc.ClientConn) {
	t.Helper()
	defer func() { require.NoError(t, conn.Close()) }()

	resp, err := helloworld.NewGreeterClient(conn).SayHello(ctx, &helloworld.HelloRequest{Name: "rob"})
	require.NoError(t, err)
	require.Equal(t, "Hello rob", resp.GetMessage())
}
