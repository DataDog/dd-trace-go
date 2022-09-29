// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kubernetes provides functions to trace k8s.io/client-go (https://github.com/kubernetes/client-go).
package kubernetes // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var (
	apiRegexp = regexp.MustCompile("^(?:/api/v1/|/apis/([a-z0-9.-]+/)([a-z0-9.-]+/))(watch/)?(namespaces/[a-z0-9.-]+/)?([a-z0-9.-]+)(/[a-z0-9.-]+)?(/[a-z0-9.-]+)?(/[a-z0-9.-]+)?$")
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
	c := apiRegexp.FindStringSubmatch(path)
	if c == nil {
		return method
	}

	var out strings.Builder
	out.WriteString(method)
	out.WriteByte(' ')

	// {group}/{version}/
	out.WriteString(c[1])
	out.WriteString(c[2])
	// watch/
	out.WriteString(c[3])
	// namespaces/{namespace}/
	if c[4] != "" {
		out.WriteString("namespaces/{namespace}/")
	}
	// {type}
	out.WriteString(c[5])
	// /{name}
	if c[6] != "" {
		out.WriteString("/{name}")
	}
	// /{subresrouce type}
	out.WriteString(c[7])
	// /{name} or /{path}
	if c[8] != "" {
		if c[7] == "/proxy" {
			out.WriteString("/{path}")
		} else {
			out.WriteString("/{name}")
		}
	}

	return out.String()
}
