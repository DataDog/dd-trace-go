// Package kubernetes provides functions to trace k8s.io/client-go (https://github.com/kubernetes/client-go).
package kubernetes // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"

import (
	"net/http"
	"strconv"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

const (
	prefixAPI   = "/api/v1/"
	prefixWatch = "watch/"
)

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Kubernetes and traces all requests.
func WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return httptrace.WrapRoundTripper(rt,
		httptrace.WithBefore(func(req *http.Request, span ddtrace.Span) {
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
}

// RequestToResource parse a kubernetes request to extract a resource name from it
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
