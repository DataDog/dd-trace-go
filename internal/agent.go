// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package internal

import (
	"net"
	"net/url"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

const (
	DefaultAgentHostname     = "localhost"
	DefaultTraceAgentPort    = "8126"
	DefaultTraceAgentUDSPath = "/var/run/datadog/apm.socket"
)

// AgentURLFromEnv resolves the URL for the trace agent based on
// the default host/port and UDS path, and via standard environment variables.
// AgentURLFromEnv has the following priority order:
//   - First, DD_TRACE_AGENT_URL if it is set
//   - Then, DD_AGENT_HOST and DD_TRACE_AGENT_PORT if either are set
//   - Then, the default UDS path given here, if the path exists
//     (the path is replacable for testing purposes, otherwise use DefaultTraceAgentUDSPath)
//   - Finally, localhost:8126
func AgentURLFromEnv(defaultSocketAPM string) *url.URL {
	if agentURL := os.Getenv("DD_TRACE_AGENT_URL"); agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL: %v", err)
		} else {
			switch u.Scheme {
			case "unix", "http", "https":
				return u
			default:
				log.Warn("Unsupported protocol %q in Agent URL %q. Must be one of: http, https, unix.", u.Scheme, agentURL)
			}
		}
	}
	var host, port string
	if v := os.Getenv("DD_AGENT_HOST"); v != "" {
		host = v
	}
	if v := os.Getenv("DD_TRACE_AGENT_PORT"); v != "" {
		port = v
	}
	if _, err := os.Stat(defaultSocketAPM); host == "" && port == "" && err == nil {
		return &url.URL{
			Scheme: "unix",
			Path:   defaultSocketAPM,
		}
	}
	if host == "" {
		host = DefaultAgentHostname
	}
	if port == "" {
		port = DefaultTraceAgentPort
	}
	return &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, port),
	}
}
