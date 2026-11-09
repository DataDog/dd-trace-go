// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package grpcdep creates gRPC clients and servers inside a dependency module.
package grpcdep

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewServer returns a gRPC server.
func NewServer() *grpc.Server {
	return grpc.NewServer()
}

// NewClient returns an insecure client connection to addr.
func NewClient(addr string) (*grpc.ClientConn, error) {
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
