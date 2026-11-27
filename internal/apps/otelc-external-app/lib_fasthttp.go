// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"net"

	"github.com/valyala/fasthttp"
)

func init() {
	registerLibrary("fasthttp", func() {
		client, server := net.Pipe()
		_ = client.Close()
		_ = fasthttp.ServeConn(server, func(*fasthttp.RequestCtx) {})
	})
}
