// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptrace

import "strings"

const (
	otherHTTPMethod = "_OTHER"
	httpMethodToken = "HTTP"
)

// NormalizeHTTPMethod applies the OpenTelemetry HTTP method conventions. It
// returns, in order, the http.request.method attribute, the {method} token for
// the span name, and http.request.method_original. Unknown methods become
// _OTHER and use HTTP in span names; case variants of known methods are
// canonicalized, with the input returned as the original method.
//
// See:
//   - https://github.com/open-telemetry/semantic-conventions/blob/7f3c3bfc300cc090871692219af6a2495aa67915/docs/http/http-spans.md?plain=1#L178-L199
//   - https://github.com/open-telemetry/semantic-conventions/blob/7f3c3bfc300cc090871692219af6a2495aa67915/docs/http/http-spans.md?plain=1#L63-L72
//   - https://github.com/open-telemetry/semantic-conventions/blob/7f3c3bfc300cc090871692219af6a2495aa67915/docs/http/http-spans.md?plain=1#L269-L273
func NormalizeHTTPMethod(method string) (attribute, spanName, original string) {
	if isKnownHTTPMethod(method) {
		return method, method, ""
	}

	canonical := strings.ToUpper(method)
	if isKnownHTTPMethod(canonical) {
		return canonical, canonical, method
	}
	if method == "" {
		return otherHTTPMethod, httpMethodToken, ""
	}
	return otherHTTPMethod, httpMethodToken, method
}

func isKnownHTTPMethod(method string) bool {
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "QUERY", "TRACE":
		return true
	default:
		return false
	}
}
