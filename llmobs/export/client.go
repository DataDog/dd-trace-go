// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/env"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	llmconfig "github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

const (
	defaultSite          = "datadoghq.com"
	defaultSpanBatchSize = 50
	defaultEvalBatchSize = 1000
	defaultMaxSpanBytes  = illmobs.SizeLimitEVPEvent
)

// ClientOption configures a [Client] built by [NewClient].
type ClientOption func(*Client) error

// WithDatadogIntake selects direct intake routing. Empty values use DD_SITE and
// DD_API_KEY.
func WithDatadogIntake(site, apiKey string) ClientOption {
	return func(c *Client) error {
		if err := setAgentless(c.config, true); err != nil {
			return err
		}
		c.config.TracerConfig.Site = site
		c.config.TracerConfig.APIKey = apiKey
		return nil
	}
}

// WithAgentURL selects Agent EVP proxy routing.
func WithAgentURL(agentURL string) ClientOption {
	return func(c *Client) error {
		if err := setAgentless(c.config, false); err != nil {
			return err
		}
		u, err := parseAgentURL(agentURL)
		if err != nil {
			return err
		}
		c.config.TracerConfig.AgentURL = u
		return nil
	}
}

// WithService sets the default service.
func WithService(service string) ClientOption {
	return func(c *Client) error {
		c.config.TracerConfig.Service = service
		return nil
	}
}

// WithEnv sets the default environment.
func WithEnv(env string) ClientOption {
	return func(c *Client) error {
		c.config.TracerConfig.Env = env
		return nil
	}
}

// WithVersion sets the default version.
func WithVersion(version string) ClientOption {
	return func(c *Client) error {
		c.config.TracerConfig.Version = version
		return nil
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) error {
		c.config.TracerConfig.HTTPClient = hc
		return nil
	}
}

// WithSpanBatchSize sets the maximum spans per request.
func WithSpanBatchSize(n int) ClientOption {
	return func(c *Client) error {
		c.spanBatch = n
		return nil
	}
}

// WithEvalBatchSize sets the maximum evaluations per request.
func WithEvalBatchSize(n int) ClientOption {
	return func(c *Client) error {
		c.evalBatch = n
		return nil
	}
}

// WithMaxSpanPayloadBytes sets the maximum encoded span request size.
func WithMaxSpanPayloadBytes(n int) ClientOption {
	return func(c *Client) error {
		c.maxSpanBytes = n
		return nil
	}
}

// Client exports completed LLM Obs spans and evaluations without starting a tracer.
type Client struct {
	transport *transport.Transport
	config    *llmconfig.Config

	spanBatch    int
	evalBatch    int
	maxSpanBytes int
}

// NewClient creates an exporter. Exactly one routing option is required.
func NewClient(mlApp string, opts ...ClientOption) (*Client, error) {
	if mlApp == "" {
		return nil, errors.New("llmobs/export: mlApp is required")
	}

	c := &Client{
		config: &llmconfig.Config{
			MLApp: mlApp,
		},
		spanBatch:    defaultSpanBatchSize,
		evalBatch:    defaultEvalBatchSize,
		maxSpanBytes: defaultMaxSpanBytes,
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if c.config.AgentlessEnabled == nil {
		return nil, errors.New("llmobs/export: a route is required: set WithDatadogIntake (direct) or WithAgentURL (via the Agent)")
	}

	tc := &c.config.TracerConfig
	if tc.Site == "" {
		tc.Site = env.Get("DD_SITE")
	}
	if tc.Site == "" {
		tc.Site = defaultSite
	}

	if tc.APIKey == "" {
		tc.APIKey = env.Get("DD_API_KEY")
	}

	c.config.ResolvedAgentlessEnabled = *c.config.AgentlessEnabled
	if c.config.ResolvedAgentlessEnabled {
		if tc.APIKey == "" {
			return nil, errors.New("llmobs/export: WithDatadogIntake requires an API key (argument or DD_API_KEY); use WithAgentURL to route via the Agent")
		}
	}

	if tc.HTTPClient == nil {
		tc.HTTPClient = c.config.DefaultHTTPClient()
	}

	c.transport = transport.New(c.config)
	c.spanBatch = orDefault(c.spanBatch, defaultSpanBatchSize)
	c.evalBatch = orDefault(c.evalBatch, defaultEvalBatchSize)
	c.maxSpanBytes = orDefault(c.maxSpanBytes, defaultMaxSpanBytes)
	return c, nil
}

func setAgentless(cfg *llmconfig.Config, enabled bool) error {
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

// WithCallMLApp overrides the default ML app for one submission.
func WithCallMLApp(mlApp string) SubmitEvaluationsOption {
	return func(sc *submitEvaluationsConfig) {
		sc.mlApp = mlApp
	}
}

func (c *Client) resolveSubmitEvaluations(opts []SubmitEvaluationsOption) submitEvaluationsConfig {
	sc := submitEvaluationsConfig{mlApp: c.config.MLApp}
	for _, opt := range opts {
		opt(&sc)
	}
	return sc
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
