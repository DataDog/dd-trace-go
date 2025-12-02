// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gosdk_test

import (
	gosdktrace "github.com/DataDog/dd-trace-go/contrib/modelcontextprotocol/go-sdk/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	server := mcp.NewServer(&mcp.Implementation{Name: "my-server", Version: "1.0.0"}, nil)
	gosdktrace.AddTracingMiddleware(server)
	_ = server
}
