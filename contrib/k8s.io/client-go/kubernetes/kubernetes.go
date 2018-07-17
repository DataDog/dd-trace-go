// Package kubernetes provides functions to trace k8s.io/client-go (https://github.com/kubernetes/client-go).
package kubernetes // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/k8s.io/client-go/kubernetes"

import (
	"net/http"
	"strings"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

const (
	apiPrefix        = "/api/v1/"
	watchPrefix      = "watch/"
	namespacesPrefix = "namespaces/"
)

// WrapRoundTripper wraps a RoundTripper intended for interfacing with
// Kubernetes and traces all requests.
func WrapRoundTripper(rt http.RoundTripper) http.RoundTripper {
	return httptrace.WrapRoundTripper(rt,
		httptrace.WithRoundTripperBefore(func(req *http.Request, span ddtrace.Span) {
			span.SetTag(ext.ServiceName, "kubernetes")
			span.SetTag(ext.ResourceName, requestToResource(req.Method, req.URL.Path))
		}))
}

func requestToResource(method, path string) string {
	resourceName := method + " "

	if !strings.HasPrefix(path, apiPrefix) {
		return resourceName
	}
	path = path[len(apiPrefix):]

	// strip out /watch
	if strings.HasPrefix(path, watchPrefix) {
		path = path[len(watchPrefix):]
		resourceName += watchPrefix
	}

	// {type}/{name}
	lastType := ""
	for i := 0; ; i++ {
		idx := strings.IndexByte(path, '/')
		if i%2 == 0 {
			// parse {type}
			if idx < 0 {
				lastType = path
			} else {
				lastType = path[:idx]
			}
			resourceName += lastType
		} else {
			// parse {name}
			resourceName += typeToPlaceholder(lastType)
		}
		if idx < 0 {
			break
		}
		path = path[idx+1:]
		resourceName += "/"
	}
	return resourceName
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
