// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

//go:generate sh -c "go run ./internal/make_responsewriter | gofmt > trace_gen.go"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

const componentName = instrumentation.PackageNetHTTP

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageNetHTTP)
}

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig = httptrace.ServeConfig

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	tw, tr, afterHandle, handled := httptrace.BeforeHandle(cfg, w, r)
	defer afterHandle()

	if handled {
		return
	}
	h.ServeHTTP(tw, tr)
}
