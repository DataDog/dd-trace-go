// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"google.golang.org/grpc/stats"
)

// NewClientStatsHandler returns a gRPC client stats.Handler to trace RPC calls.
func NewClientStatsHandler(opts ...Option) stats.Handler {
	return v2.NewClientStatsHandler(opts...)
}
