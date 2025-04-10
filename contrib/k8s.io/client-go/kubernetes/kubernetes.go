// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package kubernetes provides functions to trace k8s.io/client-go (https://github.com/kubernetes/client-go).
package kubernetes // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/k8s.io/client-go/v2/kubernetes"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
)

// WrapRoundTripperFunc creates a new WrapTransport function using the given set of
// RoundTripperOption. It is useful when desiring to enable Trace Analytics or setting
// up a RoundTripperAfterFunc.
func WrapRoundTripperFunc(opts ...httptrace.RoundTripperOption) func(http.RoundTripper) http.RoundTripper {
	return v2.WrapRoundTripperFunc(opts...)
}

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Kubernetes and traces all requests.
func WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return v2.WrapRoundTripper(rt)
}

// RequestToResource parses a Kubernetes request and extracts a resource name from it.
func RequestToResource(method, path string) string {
	return v2.RequestToResource(method, path)
}
