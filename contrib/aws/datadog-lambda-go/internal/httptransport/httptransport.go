// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package httptransport provides the transport for ddlambda's own requests to
// the Datadog Extension and the Datadog API.
package httptransport

import (
	"net"
	"net/http"
	"time"
)

// New returns a transport with the same settings as [http.DefaultTransport].
//
// It is written as an http.Transport struct literal on purpose. This package
// is in the tracer-internal list of contrib/net/http/orchestrion.client.yml,
// so Orchestrion marks the literal as tracer-internal and does not trace its
// requests. Tracing them would report each call as a span and overwrite the
// trace headers the extension reads.
func New() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
