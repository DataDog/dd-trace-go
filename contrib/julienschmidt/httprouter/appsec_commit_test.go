// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package httprouter

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
			r.Handler(http.MethodGet, "/*path", h)
			return r
		},
	})
}
