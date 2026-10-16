// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build ignore

// This file is never compiled as part of this package. The add_file rule in
// otelc.yaml copies it into github.com/valyala/fasthttp during an otelc build.
// otelc rewrites the package clause and drops the build constraint on the way
// in.
//
// The contrib imports fasthttp, so fasthttp cannot import the contrib. The
// declaration links to it instead, like the orchestrion aspect does.
package otelc

import _ "unsafe" // for go:linkname

//go:linkname __dd_fasthttptrace_wrapHandler github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2.wrapHandlerWithDefaults
func __dd_fasthttptrace_wrapHandler(RequestHandler) RequestHandler
