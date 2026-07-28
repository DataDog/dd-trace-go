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
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	defaultSite          = "datadoghq.com"
	defaultSpanBatchSize = 50
	defaultEvalBatchSize = 1000
	// defaultMaxSpanBytes is the EVP event size limit the live path enforces per
	// span event. Export applies it to the whole encoded request body, so it is
	// the stricter of the two guards and never posts a batch the SDK's own limit
	// would reject.
	defaultMaxSpanBytes = illmobs.SizeLimitEVPEvent
)

// config is the resolved, private client configuration built from ClientOptions.
type config struct {
	// routing: exactly one of the two must be selected.
	datadogSet bool
	site       string
	apiKey     string

	agentSet bool
	agentURL string

	service string
	env     string
	version string

	httpClient *http.Client

	spanBatch    int
	evalBatch    int
	maxSpanBytes int
}

// ClientOption configures a [Client] built by [NewClient].
type ClientOption func(*config)

// WithDatadogIntake routes export directly to the Datadog LLM Obs intake for
// site using apiKey in the DD-API-KEY header (agentless). Exactly one of
// WithDatadogIntake or WithAgentURL must be set.
//
// An empty site falls back to DD_SITE and then to datadoghq.com; an empty apiKey
// falls back to DD_API_KEY, so a process already configured for the rest of the
// SDK does not have to read the environment itself.
func WithDatadogIntake(site, apiKey string) ClientOption {
	return func(c *config) {
		c.datadogSet = true
		c.site = site
		c.apiKey = apiKey
	}
}

// WithAgentURL routes export through a Datadog Agent's EVP proxy at agentURL
// (e.g. "http://localhost:8126") instead of the direct Datadog intake; no
// Datadog auth is injected. Exactly one of WithDatadogIntake or WithAgentURL
// must be set.
func WithAgentURL(agentURL string) ClientOption {
	return func(c *config) {
		c.agentSet = true
		c.agentURL = agentURL
	}
}

// WithService sets the default service stamped onto spans that don't carry one
// (as the top-level service field and a service: tag).
func WithService(service string) ClientOption {
	return func(c *config) { c.service = service }
}

// WithEnv sets the default env: tag stamped onto spans that don't carry one.
func WithEnv(env string) ClientOption {
	return func(c *config) { c.env = env }
}

// WithVersion sets the default version: tag stamped onto spans that don't carry one.
func WithVersion(version string) ClientOption {
	return func(c *config) { c.version = version }
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *config) { c.httpClient = hc }
}

// WithSpanBatchSize sets the max spans per request (default 50).
func WithSpanBatchSize(n int) ClientOption {
	return func(c *config) { c.spanBatch = n }
}

// WithEvalBatchSize sets the max evaluations per request (default 1000).
func WithEvalBatchSize(n int) ClientOption {
	return func(c *config) { c.evalBatch = n }
}

// WithMaxSpanPayloadBytes sets the max encoded span-request size before an
// oversized batch is bisected and, as a last resort, a single span's
// input/output values are truncated. It defaults to the EVP event size limit the
// live path enforces per span event, applied here to the whole request body.
// Truncation is best-effort: only input/output are shrunk, so payloads dominated
// by other fields may still exceed the limit.
func WithMaxSpanPayloadBytes(n int) ClientOption {
	return func(c *config) { c.maxSpanBytes = n }
}

// Client is an offline exporter for LLM Obs spans and evaluations. It targets
// exactly one destination; build several clients for multi-destination export.
// It is safe for concurrent use.
type Client struct {
	transport *transport.Transport

	service string
	env     string
	version string
	mlApp   string

	spanBatch    int
	evalBatch    int
	maxSpanBytes int
}

// NewClient builds a Client for the ML app mlApp, which is required: the live
// client rejects an empty ml_app, and requiring it here means every exported span
// and evaluation carries one.
//
// mlApp is a default. A span overrides it with an "ml_app:<value>" entry in
// [SpanEvent.Tags]; an evaluation overrides it with [EvaluationMetric.MLApp], or
// per call with [WithCallMLApp].
//
// Exactly one routing option ([WithDatadogIntake] or [WithAgentURL]) must be
// supplied.
func NewClient(mlApp string, opts ...ClientOption) (*Client, error) {
	if mlApp == "" {
		return nil, errors.New("llmobs/export: mlApp is required")
	}

	cfg := &config{
		spanBatch:    defaultSpanBatchSize,
		evalBatch:    defaultEvalBatchSize,
		maxSpanBytes: defaultMaxSpanBytes,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	switch {
	case cfg.datadogSet && cfg.agentSet:
		return nil, errors.New("llmobs/export: set exactly one route: WithDatadogIntake or WithAgentURL, not both")
	case !cfg.datadogSet && !cfg.agentSet:
		return nil, errors.New("llmobs/export: a route is required: set WithDatadogIntake (direct) or WithAgentURL (via the Agent)")
	}

	site := cfg.site
	if site == "" {
		site = env.Get("DD_SITE")
	}
	if site == "" {
		site = defaultSite
	}

	apiKey := cfg.apiKey
	if apiKey == "" {
		apiKey = env.Get("DD_API_KEY")
	}

	var agentURL *url.URL
	if cfg.datadogSet {
		if apiKey == "" {
			return nil, errors.New("llmobs/export: WithDatadogIntake requires an API key (argument or DD_API_KEY); use WithAgentURL to route via the Agent")
		}
	} else {
		// Trim a trailing slash so the EVP proxy path is not doubled.
		u, err := url.Parse(strings.TrimRight(cfg.agentURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("llmobs/export: invalid agent URL: %w", err)
		}
		// Require a supported scheme with a host (or unix socket path). Otherwise a
		// value like "localhost:8126" or a typo like "htt://host" parses but every
		// export fails later in Post with "unsupported protocol scheme".
		switch u.Scheme {
		case "http", "https":
			if u.Host == "" {
				return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: missing host", cfg.agentURL)
			}
		case "unix":
			if u.Path == "" {
				return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: missing unix socket path", cfg.agentURL)
			}
		default:
			return nil, fmt.Errorf("llmobs/export: invalid agent URL %q: scheme must be http, https, or unix", cfg.agentURL)
		}
		agentURL = u
	}

	icfg := &llmconfig.Config{
		ResolvedAgentlessEnabled: cfg.datadogSet,
		TracerConfig: llmconfig.TracerConfig{
			Site:       site,
			APIKey:     apiKey,
			AgentURL:   agentURL,
			HTTPClient: cfg.httpClient,
			Service:    cfg.service,
			Env:        cfg.env,
			Version:    cfg.version,
		},
	}
	if icfg.TracerConfig.HTTPClient == nil {
		icfg.TracerConfig.HTTPClient = icfg.DefaultHTTPClient()
	}

	c := &Client{
		transport:    transport.New(icfg),
		service:      cfg.service,
		env:          cfg.env,
		version:      cfg.version,
		mlApp:        mlApp,
		spanBatch:    orDefault(cfg.spanBatch, defaultSpanBatchSize),
		evalBatch:    orDefault(cfg.evalBatch, defaultEvalBatchSize),
		maxSpanBytes: orDefault(cfg.maxSpanBytes, defaultMaxSpanBytes),
	}
	return c, nil
}

// SubmitSpansOption customizes a single [Client.SubmitSpans] call.
type SubmitSpansOption func(*submitSpansConfig)

type submitSpansConfig struct {
	service string
}

// WithCallService overrides the client's default service for this SubmitSpans
// call only (stamped as the top-level service field and the service: tag).
func WithCallService(service string) SubmitSpansOption {
	return func(sc *submitSpansConfig) {
		sc.service = service
	}
}

func (c *Client) resolveSubmitSpans(opts []SubmitSpansOption) submitSpansConfig {
	sc := submitSpansConfig{service: c.service}
	for _, opt := range opts {
		opt(&sc)
	}
	return sc
}

// SubmitEvaluationsOption customizes a single [Client.SubmitEvaluations] call. It
// is a distinct type from [SubmitSpansOption] so an option that has no meaning
// for evaluations fails to compile instead of being silently ignored.
type SubmitEvaluationsOption func(*submitEvaluationsConfig)

type submitEvaluationsConfig struct {
	mlApp string
}

// WithCallMLApp overrides the client's default ML app for this SubmitEvaluations
// call only. A per-metric MLApp still wins over it.
func WithCallMLApp(mlApp string) SubmitEvaluationsOption {
	return func(sc *submitEvaluationsConfig) {
		sc.mlApp = mlApp
	}
}

func (c *Client) resolveSubmitEvaluations(opts []SubmitEvaluationsOption) submitEvaluationsConfig {
	sc := submitEvaluationsConfig{mlApp: c.mlApp}
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

// tracerVersion is the value stamped into the ddtrace.version tag on every
// exported span and evaluation metric. The envelope's _dd.tracer_version comes
// from the shared transport builder, not from here.
func tracerVersion() string {
	return version.Tag
}
