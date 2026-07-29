// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package export submits completed LLM Observability data without starting a tracer.
//
// EXPERIMENTAL: This package may change or be removed without notice.
package export

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	llmconfig "github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

const (
	defaultSpanBatchSize = 50
	defaultEvalBatchSize = 1000
)

type clientConfig = llmconfig.Config

// ClientOption configures a [Client] built by [NewClient].
type ClientOption func(*clientConfig) error

// WithDatadogIntake selects direct intake routing. Empty values use DD_SITE and
// DD_API_KEY.
func WithDatadogIntake(site, apiKey string) ClientOption {
	return func(cfg *clientConfig) error {
		if err := setAgentless(cfg, true); err != nil {
			return err
		}
		cfg.TracerConfig.Site = site
		cfg.TracerConfig.APIKey = apiKey
		return nil
	}
}

// WithAgentURL selects Agent EVP proxy routing.
func WithAgentURL(agentURL string) ClientOption {
	return func(cfg *clientConfig) error {
		if err := setAgentless(cfg, false); err != nil {
			return err
		}
		u, err := parseAgentURL(agentURL)
		if err != nil {
			return err
		}
		cfg.TracerConfig.AgentURL = u
		return nil
	}
}

// WithService sets the default service.
func WithService(service string) ClientOption {
	return func(cfg *clientConfig) error {
		cfg.TracerConfig.Service = service
		return nil
	}
}

// WithEnv sets the default environment.
func WithEnv(env string) ClientOption {
	return func(cfg *clientConfig) error {
		cfg.TracerConfig.Env = env
		return nil
	}
}

// WithVersion sets the default version.
func WithVersion(version string) ClientOption {
	return func(cfg *clientConfig) error {
		cfg.TracerConfig.Version = version
		return nil
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(cfg *clientConfig) error {
		cfg.TracerConfig.HTTPClient = hc
		return nil
	}
}

// Client submits completed LLM Obs spans and evaluations without starting a tracer.
type Client struct {
	transport *transport.Transport
	config    *llmconfig.Config
}

// NewClient creates a client. Exactly one routing option is required.
func NewClient(mlApp string, opts ...ClientOption) (*Client, error) {
	if mlApp == "" {
		return nil, errors.New("llmobs/export: mlApp is required")
	}

	cfg := &clientConfig{MLApp: mlApp}
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	if cfg.AgentlessEnabled == nil {
		return nil, errors.New("llmobs/export: a route is required: set WithDatadogIntake (direct) or WithAgentURL (via the Agent)")
	}

	tc := &cfg.TracerConfig
	if tc.Site == "" {
		tc.Site = env.Get("DD_SITE")
	}

	if tc.APIKey == "" {
		tc.APIKey = env.Get("DD_API_KEY")
	}

	cfg.ResolvedAgentlessEnabled = *cfg.AgentlessEnabled
	if cfg.ResolvedAgentlessEnabled {
		if !llmconfig.IsAPIKeyValid(tc.APIKey) {
			return nil, errors.New("llmobs/export: WithDatadogIntake requires a valid API key (argument or DD_API_KEY); use WithAgentURL to route via the Agent")
		}
	}

	if tc.HTTPClient == nil {
		tc.HTTPClient = cfg.DefaultHTTPClient()
	}

	return &Client{
		transport: transport.New(cfg),
		config:    cfg,
	}, nil
}

func setAgentless(cfg *clientConfig, enabled bool) error {
	if cfg.AgentlessEnabled != nil && *cfg.AgentlessEnabled != enabled {
		return errors.New("llmobs/export: set exactly one route: WithDatadogIntake or WithAgentURL, not both")
	}
	cfg.AgentlessEnabled = &enabled
	return nil
}

func parseAgentURL(agentURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(agentURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("llmobs/export: invalid agent URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		if u.Host == "" {
			return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: missing host", agentURL)
		}
	case "unix":
		if u.Path == "" {
			return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: missing unix socket path", agentURL)
		}
	default:
		return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: scheme must be http, https, or unix", agentURL)
	}
	return u, nil
}

// SubmitSpansOption customizes a single [Client.SubmitSpans] call.
type SubmitSpansOption func(*submitSpansConfig)

type submitSpansConfig struct {
	service string
}

// WithCallService overrides the default service for one submission.
func WithCallService(service string) SubmitSpansOption {
	return func(sc *submitSpansConfig) {
		sc.service = service
	}
}

func (c *Client) resolveSubmitSpans(opts []SubmitSpansOption) submitSpansConfig {
	sc := submitSpansConfig{service: c.config.TracerConfig.Service}
	for _, opt := range opts {
		opt(&sc)
	}
	return sc
}

// SubmitEvaluationsOption customizes one evaluation submission.
type SubmitEvaluationsOption func(*submitEvaluationsConfig)

type submitEvaluationsConfig struct {
	mlApp string
}

// WithCallMLApp sets a non-empty ML app override for one submission.
func WithCallMLApp(mlApp string) SubmitEvaluationsOption {
	return func(sc *submitEvaluationsConfig) {
		if mlApp != "" {
			sc.mlApp = mlApp
		}
	}
}

func (c *Client) resolveSubmitEvaluations(opts []SubmitEvaluationsOption) submitEvaluationsConfig {
	sc := submitEvaluationsConfig{mlApp: c.config.MLApp}
	for _, opt := range opts {
		opt(&sc)
	}
	return sc
}
