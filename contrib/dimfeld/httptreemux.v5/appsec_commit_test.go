// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httptreemux

import (
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/appsecwire"
)

// TestAppSecResponseCommit runs the case table of appsecwire. It uses a real
// server and the real WAF.
func TestAppSecResponseCommit(t *testing.T) {
	appsecwire.RunMatrix(t, appsecwire.Contrib{
		Mount: func(h http.Handler) http.Handler {
			r := New()
			serve := func(w http.ResponseWriter, req *http.Request, _ map[string]string) {
				h.ServeHTTP(w, req)
			}
			// The catch-all route does not match the root path.
			r.GET("/", serve)
			r.GET("/*path", serve)
			return r
		},
	})
}
