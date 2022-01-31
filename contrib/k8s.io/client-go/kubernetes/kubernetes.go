// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kubernetes provides functions to trace k8s.io/client-go (https://github.com/kubernetes/client-go).
package kubernetes // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"

import (
	"net/http"
	"strconv"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	prefixAPI   = "/api/v1/"
	prefixWatch = "watch/"
)

// WrapRoundTripperFunc creates a new WrapTransport function using the given set of
// RountripperOption. It is useful when desiring to to enable Trace Analytics or setting
// up a RoundTripperAfterFunc.
func WrapRoundTripperFunc(opts ...httptrace.RoundTripperOption) func(http.RoundTripper) http.RoundTripper {
	return func(rt http.RoundTripper) http.RoundTripper {
		return wrapRoundTripperWithOptions(rt, opts...)
	}
}

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Kubernetes and traces all requests.
func WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return wrapRoundTripperWithOptions(rt)
}

func wrapRoundTripperWithOptions(rt http.RoundTripper, opts ...httptrace.RoundTripperOption) http.RoundTripper {
	opts = append(opts, httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
		span.SetTag(ext.ServiceName, "kubernetes")
		span.SetTag(ext.ResourceName, RequestToResource(req.Method, req.URL.Path))
		traceID := span.Context().TraceID()
		if traceID == 0 {
			// tracer is not running
			return
		}
		kubeAuditID := strconv.FormatUint(traceID, 10)
		req.Header.Set("Audit-Id", kubeAuditID)
		span.SetTag("kubernetes.audit_id", kubeAuditID)
	}))
	log.Debug("contrib/k8s.io/client-go/kubernetes: Wrapping RoundTripper.")
	return httptrace.WrapRoundTripper(rt, opts...)
}

// RequestToResource parses a Kubernetes request and extracts a resource name from it.
func RequestToResource(method, path string) string {
	if !strings.HasPrefix(path, prefixAPI) {
		return method
	}

	var out strings.Builder
	out.WriteString(method)
	out.WriteByte(' ')

	path = strings.TrimPrefix(path, prefixAPI)

	if strings.HasPrefix(path, prefixWatch) {
		// strip out /watch
		path = strings.TrimPrefix(path, prefixWatch)
		out.WriteString(prefixWatch)
	}

	// {type}/{name}
	var lastType string
	for i, str := range strings.Split(path, "/") {
		if i > 0 {
			out.WriteByte('/')
		}
		if i%2 == 0 {
			lastType = str
			out.WriteString(lastType)
		} else {
			// parse {name}
			out.WriteString(typeToPlaceholder(lastType))
		}
	}
	return out.String()
}

func typeToPlaceholder(typ string) string {
	switch typ {
	case "namespaces":
		return "{namespace}"
	case "proxy":
		return "{path}"
	default:
		return "{name}"
	}
}
