// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"errors"
	"net/url"
	"strings"
)

const (
	// agentlessConfigPath is the canonical path of the managed Agentless
	// configuration endpoint.
	agentlessConfigPath = "/api/v2/feature-flagging/config/rules-based/server"
	// agentlessHostPrefix is prepended to the (lowercased, trimmed) site to
	// build the managed endpoint's host.
	agentlessHostPrefix = "ufc-server.ff-cdn."
	// agentlessDefaultSite is used when no site is configured.
	agentlessDefaultSite = "datadoghq.com"
)

// Sentinel errors returned by buildAgentlessEndpoint. None of them interpolate
// the configured URL or site, since either may embed credentials.
var (
	errAgentlessNoAPIKey      = errors.New("agentless: no API key configured for the managed endpoint")
	errAgentlessInvalidURL    = errors.New("agentless: invalid base URL")
	errAgentlessScheme        = errors.New("agentless: base URL scheme must be http or https")
	errAgentlessURLWhitespace = errors.New("agentless: base URL contains whitespace")
	errAgentlessInvalidSite   = errors.New("agentless: invalid site")
)

// agentlessEndpoint is a resolved Agentless configuration endpoint.
type agentlessEndpoint struct {
	// url is the full request URL. SENSITIVE: may embed credentials; never log.
	url string
	// managed reports whether this is the Datadog-hosted endpoint, the only
	// one that receives the DD-API-KEY header.
	managed bool
}

// buildAgentlessEndpoint resolves the Agentless configuration endpoint. When
// baseURL is blank, it derives the managed Datadog-hosted endpoint from site
// and requires apiKey. When baseURL is set, it is used verbatim (with the
// canonical path appended only if the URL has no path of its own) and the
// endpoint never receives the API key.
func buildAgentlessEndpoint(baseURL, site, env, apiKey string) (agentlessEndpoint, error) {
	if strings.TrimSpace(baseURL) != "" {
		return buildCustomAgentlessEndpoint(baseURL)
	}
	return buildManagedAgentlessEndpoint(site, env, apiKey)
}

func buildManagedAgentlessEndpoint(site, env, apiKey string) (agentlessEndpoint, error) {
	if apiKey == "" {
		return agentlessEndpoint{}, errAgentlessNoAPIKey
	}

	s := strings.ToLower(strings.TrimSpace(site))
	if s == "" {
		s = agentlessDefaultSite
	}
	if containsWhitespace(s) || strings.ContainsAny(s, "/?#@:") || strings.Contains(s, "://") {
		return agentlessEndpoint{}, errAgentlessInvalidSite
	}

	u := url.URL{
		Scheme: "https",
		Host:   agentlessHostPrefix + s,
		Path:   agentlessConfigPath,
	}
	if env != "" {
		u.RawQuery = url.Values{"dd_env": {env}}.Encode()
	}

	return agentlessEndpoint{url: u.String(), managed: true}, nil
}

func buildCustomAgentlessEndpoint(baseURL string) (agentlessEndpoint, error) {
	if containsWhitespace(baseURL) {
		return agentlessEndpoint{}, errAgentlessURLWhitespace
	}

	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return agentlessEndpoint{}, errAgentlessInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return agentlessEndpoint{}, errAgentlessScheme
	}

	if u.Path == "" || u.Path == "/" {
		u.Path = agentlessConfigPath
	}
	// Any other path is kept verbatim, including its query string, and
	// dd_env is never appended to a custom endpoint.

	return agentlessEndpoint{url: u.String(), managed: false}, nil
}

func containsWhitespace(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
	}) >= 0
}
