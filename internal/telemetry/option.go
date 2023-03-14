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

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

// An Option is used to configure the telemetry client's settings
type Option func(*Client)

// ApplyOps sets various fields of the client
func (c *Client) ApplyOps(opts ...Option) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, opt := range opts {
		opt(c)
	}
}

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
					client.disabled = true
				}
			}
			client.URL = getAgentlessURL()
		} else {
			u, err := url.Parse(agentURL)
			if err == nil {
				u.Path = "/telemetry/proxy/api/v2/apmtelemetry"
				client.URL = u.String()
			} else {
				client.log("Agent URL %s is invalid, not starting telemetry", agentURL)
				client.disabled = true
			}
		}
	}
}

func getAgentlessURL() string {
	agentlessEndpointLock.RLock()
	defer agentlessEndpointLock.RUnlock()
	return agentlessURL
}

// configEnvFallback returns the value of environment variable with the
// given key if def == ""
func configEnvFallback(key, def string) string {
	if def != "" {
		return def
	}
	return os.Getenv(key)
}

// applyFallbackOps applies default values to the client unless
// those values are already set.
func (c *Client) applyFallbackOps() {
	if c.Client == nil {
		WithHTTPClient(defaultHTTPClient)(c)
	}
	if len(c.APIKey) == 0 {
		WithAPIKey(defaultAPIKey())(c)
	}
	c.Service = configEnvFallback("DD_SERVICE", c.Service)
	if len(c.Service) == 0 {
		if name := globalconfig.ServiceName(); len(name) != 0 {
			c.Service = name
		} else {
			// I think service *has* to be something?
			c.Service = "unnamed-go-service"
		}
	}
	c.Env = configEnvFallback("DD_ENV", c.Env)
	c.Version = configEnvFallback("DD_VERSION", c.Version)
	if len(c.metrics) == 0 {
		// XXX: Should we let metrics persist between starting and stopping?
		c.metrics = make(map[Namespace]map[string]*metric)
	}
}

// SetAgentlessEndpoint is used for testing purposes to replace the real agentless
// endpoint with a custom one
func SetAgentlessEndpoint(endpoint string) string {
	agentlessEndpointLock.Lock()
	defer agentlessEndpointLock.Unlock()
	prev := agentlessURL
	agentlessURL = endpoint
	return prev
}
