// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
)

const defaultMaxAttempts uint = 3

type route uint8

const (
	routeUnset route = iota
	routeDatadog
	routeCollector
)

type clientConfig struct {
	route                 route
	site                  string
	apiKey                string
	endpoint              *url.URL
	httpClient            *http.Client
	headers               map[string]string
	maxAttempts           uint
	requestTimeout        time.Duration
	defaultRequestTimeout time.Duration
}

// ClientOption configures a [Client] built by [NewClient].
type ClientOption func(*clientConfig) error

// WithDatadogIntake selects direct Datadog intake routing. Empty values use the
// global site and API key configuration.
func WithDatadogIntake(site, apiKey string) ClientOption {
	return func(cfg *clientConfig) error {
		if err := setRoute(cfg, routeDatadog); err != nil {
			return err
		}
		cfg.site = strings.TrimSpace(site)
		cfg.apiKey = strings.TrimSpace(apiKey)
		return nil
	}
}

// WithCollectorEndpoint selects an OTLP/HTTP collector or Agent base URL. The
// client appends the standard signal paths to this URL. Use [WithHeaders] for
// collector authentication.
func WithCollectorEndpoint(endpoint string) ClientOption {
	return func(cfg *clientConfig) error {
		if err := setRoute(cfg, routeCollector); err != nil {
			return err
		}
		u, err := parseEndpoint(endpoint)
		if err != nil {
			return err
		}
		cfg.endpoint = u
		return nil
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(cfg *clientConfig) error {
		cfg.httpClient = client
		return nil
	}
}

// WithHeaders adds headers to each request. Protocol and Datadog routing headers
// take precedence over values with the same names.
func WithHeaders(headers map[string]string) ClientOption {
	return func(cfg *clientConfig) error {
		if cfg.headers == nil {
			cfg.headers = make(map[string]string, len(headers))
		}
		maps.Copy(cfg.headers, headers)
		return nil
	}
}

// WithMaxAttempts sets the maximum number of HTTP attempts per request,
// including the initial attempt. The default is three.
func WithMaxAttempts(attempts uint) ClientOption {
	return func(cfg *clientConfig) error {
		if attempts == 0 {
			return errors.New("otlp/export: max attempts must be at least 1")
		}
		cfg.maxAttempts = attempts
		return nil
	}
}

// WithRequestTimeout sets a timeout for each HTTP attempt. Without it, an
// existing context deadline is preserved, and the global Agent timeout applies
// when the context has no deadline.
func WithRequestTimeout(timeout time.Duration) ClientOption {
	return func(cfg *clientConfig) error {
		if timeout <= 0 {
			return errors.New("otlp/export: request timeout must be positive")
		}
		cfg.requestTimeout = timeout
		return nil
	}
}

func resolveClientConfig(opts []ClientOption) (*clientConfig, error) {
	global := internalconfig.Get()
	cfg := &clientConfig{
		maxAttempts:           defaultMaxAttempts,
		defaultRequestTimeout: global.AgentTimeout(),
	}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	switch cfg.route {
	case routeDatadog:
		if cfg.site == "" {
			cfg.site = global.Site()
		}
		if cfg.apiKey == "" {
			cfg.apiKey = global.APIKey()
		}
		if !internal.IsAPIKeyValid(cfg.apiKey) {
			return nil, errors.New("otlp/export: WithDatadogIntake requires a valid API key (argument or DD_API_KEY)")
		}
		endpoint, err := parseEndpoint("https://otlp." + cfg.site)
		if err != nil || endpoint.Path != "" || endpoint.Host != "otlp."+cfg.site {
			return nil, fmt.Errorf("otlp/export: invalid Datadog site %q", cfg.site)
		}
		cfg.endpoint = endpoint
	case routeCollector:
		if cfg.endpoint == nil {
			return nil, errors.New("otlp/export: WithCollectorEndpoint requires an endpoint")
		}
	default:
		return nil, errors.New("otlp/export: a route is required: set WithDatadogIntake or WithCollectorEndpoint")
	}

	if cfg.httpClient == nil {
		cfg.httpClient = internal.DefaultHTTPClient(0, false)
	}
	client := *cfg.httpClient
	client.CheckRedirect = noRedirect
	cfg.httpClient = &client
	return cfg, nil
}

func setRoute(cfg *clientConfig, selected route) error {
	if cfg.route != routeUnset && cfg.route != selected {
		return errors.New("otlp/export: set exactly one route: WithDatadogIntake or WithCollectorEndpoint, not both")
	}
	cfg.route = selected
	return nil
}

func parseEndpoint(endpoint string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("otlp/export: invalid endpoint %q: must be an http(s) URL with a host", endpoint)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("otlp/export: invalid endpoint %q: query and fragment are not allowed", endpoint)
	}
	return u, nil
}
