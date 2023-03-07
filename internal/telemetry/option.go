// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package telemetry

import (
	"net/http"
	"net/url"
	"os"
	"unicode"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

// An Option is used to configure the telemetry client's settings
type Option func(*Client)

// WithNamespace sets name as the telemetry client's namespace (tracer, profiler, appsec)
func WithNamespace(name Namespace) Option {
	return func(client *Client) {
		client.Namespace = name
	}
}

// WithEnv sets the app specific environment for the telemetry client
func WithEnv(env string) Option {
	return func(client *Client) {
		client.Env = env
	}
}

// WithService sets the app specific service for the telemetry client
func WithService(service string) Option {
	return func(client *Client) {
		client.Service = service
	}
}

// WithVersion sets the app specific version for the telemetry client
func WithVersion(version string) Option {
	return func(client *Client) {
		client.Version = version
	}
}

// WithHTTPClient specifies the http client for the telemetry client
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.Client = httpClient
	}
}

// isAPIKeyValid reports whether the given string is a structurally valid API key
// (copied from profiler)
func isAPIKeyValid(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, c := range key {
		if c > unicode.MaxASCII || (!unicode.IsLower(c) && !unicode.IsNumber(c)) {
			return false
		}
	}
	return true
}

func defaultAPIKey() string {
	if v := os.Getenv("DD_API_KEY"); isAPIKeyValid(v) {
		return v
	}
	return ""
}

// WithAPIKey sets the DD API KEY for the telemetry client
func WithAPIKey(v string) Option {
	return func(client *Client) {
		if isAPIKeyValid(v) {
			client.APIKey = v
		}
	}
}

// WithURL sets the URL for where telemetry information is flushed to.
// For the URL, uploading through agent goes through
//
//	${AGENT_URL}/telemetry/proxy/api/v2/apmtelemetry
//
// for agentless:
//
//	https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry
//
// with an API key
func WithURL(agentless bool, agentURL string) Option {
	return func(client *Client) {
		if agentless {
			if client.APIKey == "" {
				// set the api key if APIKey field is blank for the client.
				// WithAPIKey only sets the APIKey field if the key passed in is valid
				// else, it does nothing
				WithAPIKey(defaultAPIKey())(client)
				if client.APIKey == "" {
					client.log("Agentless is turned on, but valid DD API key was not found. Not starting telemetry")
					client.Disabled = true
				}
			}
			client.URL = agentlessURL
		} else {
			// TODO: check agent /info endpoint to see if the agent is
			// sufficiently recent to support this endpoint? overkill?
			u, err := url.Parse(agentURL)
			if err == nil {
				u.Path = "/telemetry/proxy/api/v2/apmtelemetry"
				client.URL = u.String()
			} else {
				client.log("Agent URL %s is invalid, not starting telemetry", agentURL)
				client.Disabled = true
			}
		}
	}
}

func defaultClient() (client *Client) {
	client = new(Client)
	client.Disabled = !internal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true)
	client.CollectDependencies = internal.BoolEnv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", true)
	WithHTTPClient(defaultHTTPClient)(client)
	WithAPIKey(defaultAPIKey())(client)
	return client
}
