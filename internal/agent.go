// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal

import (
	"net/url"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// AgentURLFromEnv determines the trace agent URL from environment variable
// DD_TRACE_AGENT_URL. If the determined value is valid and not a UDS socket,
// it returns the value and false. If the determined value is valid and a UDS
// socket, it returns the UDS path and true. If the value is not valid, it returns
// an empty string and false.
func AgentURLFromEnv() (string, bool) {
	agentURL := os.Getenv("DD_TRACE_AGENT_URL")
	if agentURL == "" {
		return "", false
	}
	u, err := url.Parse(agentURL)
	if err != nil {
		log.Warn("Failed to parse DD_TRACE_AGENT_URL: %v", err)
		return "", false
	}
	// Return the UDS path to include in the HTTP client
	// Transport's DialContext.
	if u.Scheme == "unix" {
		if u.Path == "" {
			return "", false
		}
		return u.Path, true
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		log.Warn("Unsupported protocol '%s' in Agent URL '%s'. Must be one of: http, https, unix", u.Scheme, agentURL)
		return "", false
	}
	return agentURL, false
}
