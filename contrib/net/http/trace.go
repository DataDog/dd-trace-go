// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http // import "github.com/DataDog/dd-trace-go/contrib/net/http/v2"

import (
	"net/http"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
)

// ServeConfig specifies the tracing configuration when using TraceAndServe.
type ServeConfig = httptrace.ServeConfig

// TraceAndServe serves the handler h using the given ResponseWriter and Request, applying tracing
// according to the specified config.
func TraceAndServe(h http.Handler, w http.ResponseWriter, r *http.Request, cfg *ServeConfig) {
	wrap.TraceAndServe(h, w, r, cfg)
}
