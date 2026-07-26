// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package internal

import (
	"net"
	"net/http"
	"time"

	// OTel did a breaking change to the module go.opentelemetry.io/collector/pdata which is imported by the agent
	// and go.opentelemetry.io/collector/pdata/pprofile depends on it and is breaking because of it
	// For some reason the dependency closure won't let use upgrade this module past the point where it does not break anymore
	// So we are forced to add a blank import of this module to give us back the control over its version
	//
	// TODO: remove this once github.com/datadog-agent/pkg/trace has upgraded both modules past the breaking change
	_ "go.opentelemetry.io/collector/pdata/pprofile"
)

const (
	DefaultAgentHostname  = "localhost"
	DefaultTraceAgentPort = "8126"
)

// This is a variable rather than a constant so it can be replaced in unit tests
var DefaultTraceAgentUDSPath = "/var/run/datadog/apm.socket"

func DefaultDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}
}

func DefaultHTTPClient(timeout time.Duration, disableKeepAlives bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           DefaultDialer(timeout).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     disableKeepAlives,
		},
		Timeout: timeout,
	}
}
