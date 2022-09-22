// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package agent provides tools for interacting with the Datdog agent, including
// automatically discovering the Datadog agent and its available features
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	// defaultSocketAPM specifies the socket path to use for connecting to the trace-agent.
	defaultSocketAPM   = "/var/run/datadog/apm.socket"
	defaultHostname    = "localhost"
	defaultPort        = "8126"
	defaultHTTPTimeout = 2 * time.Second
)

// Features is functionality reported as available by the trace-agent
type Features struct {
	// Endpoints are the available data ingestion endpoints such as /traces,
	// /profiles, etc.
	Endpoints []string `json:"endpoints"`
	// ClientDropP0s indicates whether the tracer client should [ ... SOMETHING ... ]
	ClientDropP0s bool `json:"client_drop_p0s"`
	// StatsdPort is the UDP port where the agent accepts DogStatsD metrics
	StatsdPort int `json:"statsd_port"`
	// FeatureFlags are enabled agent feature flags
	FeatureFlags []string `json:"feature_flags"`
}

// LoadFeatures queries the agent's /info endpoint
func LoadFeatures(addr string, client *http.Client) (*Features, error) {
	u := url.URL{
		Scheme: "http",
		Host:   addr,
		Path:   "/info",
	}
	fmt.Fprintf(os.Stderr, "trying URL %s\n", u.String())
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("loading features: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// agent is older than 7.28.0, features not discoverable
		return nil, errors.New("agent does not support /info endpoint")
	}
	defer resp.Body.Close()
	var info Features
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding features: %w", err)
	}
	return &info, nil
}

var (
	defaultDialer = &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		DualStack: true,
	}

	defaultClient = &http.Client{
		// We copy the transport to avoid using the default one, as it might be
		// augmented with tracing and we don't want these calls to be recorded.
		// See https://golang.org/pkg/net/http/#DefaultTransport .
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           defaultDialer.DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: defaultHTTPTimeout,
	}
)

// HTTPClient returns the default http.Client for interacting with the agent
func HTTPClient() *http.Client {
	var sockaddr string
	if v := os.Getenv("DD_TRACE_AGENT_URL"); v != "" {
		if u, err := url.Parse(v); err == nil && u.Scheme == "unix" {
			sockaddr = u.Path
		}
	} else if os.Getenv("DD_AGENT_HOST") == "" && os.Getenv("DD_TRACE_AGENT_PORT") == "" {
		sockaddr = defaultSocketAPM
	}
	if _, err := os.Stat(sockaddr); err == nil {
		// we have the UDS socket file, use it
		return UDSClient(sockaddr)
	}
	return defaultClient
}

// UDSClient returns a new http.Client which connects using the given UDS socket path.
func UDSClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return defaultDialer.DialContext(ctx, "unix", (&net.UnixAddr{
					Name: socketPath,
					Net:  "unix",
				}).String())
			},
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: defaultHTTPTimeout,
	}
}

// ResolveAgentAddr attempts to discover the trace-agent's address (host:port,
// or hostname if full agent URL is given) from the environment, and falls back
// to default localhost:8126
func ResolveAgentAddr() string {
	if v := os.Getenv("DD_TRACE_AGENT_URL"); v != "" {
		if u, err := url.Parse(v); err == nil {
			switch u.Scheme {
			case "http", "https":
				return u.Host
			default:
				// If the scheme is "unix", fall back to the
				// default values. Whatever we give for the host
				// will be ignored anyway by the HTTP client
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
	if host == "" {
		host = defaultHostname
	}
	if port == "" {
		port = defaultPort
	}

	return net.JoinHostPort(host, port)
}
