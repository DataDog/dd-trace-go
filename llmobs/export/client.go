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

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	defaultSite          = "datadoghq.com"
	defaultSpanBatchSize = 50
	defaultEvalBatchSize = 1000
	defaultMaxSpanBytes  = 5_000_000 // 5 MB, matching internal/llmobs sizeLimitEVPEvent (the EVP event size limit)
)

// Config configures a [Client]. A client targets exactly one destination; build
// several clients for multi-destination export.
//
// Routing:
//   - Datadog route (default): leave AgentURL empty and set Site + APIKey. The
//     client posts directly to the LLM Obs intake derived from Site with the
//     DD-API-KEY header.
//   - Agent route: set AgentURL to a Datadog Agent base URL. The client posts
//     through the Agent's EVP proxy and does not inject Datadog auth.
type Config struct {
	// Site is the Datadog site (e.g. "datadoghq.com"). Defaults to datadoghq.com.
	Site string
	// APIKey is the Datadog API key, required for the Datadog route.
	APIKey string
	// AgentURL, if set, routes export through the Agent EVP proxy instead of the
	// direct Datadog intake (e.g. "http://localhost:8126").
	AgentURL string

	// Service, Env and Version are stamped onto spans when absent (Service as the
	// top-level service field; Env and Version as env:/version: tags).
	Service string
	Env     string
	Version string
	// MLApp is the ML app name (required). It is stamped as the ml_app tag on
	// spans that don't carry one and is the default ml_app for evaluations.
	MLApp string

	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client

	// SpanBatchSize is the max spans per request (default 50).
	SpanBatchSize int
	// EvalBatchSize is the max evaluations per request (default 1000).
	EvalBatchSize int
	// MaxSpanPayloadBytes is the max encoded span-request size before input/output
	// values are truncated (default 5 MB, the EVP event size limit). Truncation is best-effort: only
	// input/output are shrunk, so payloads dominated by other fields may still
	// exceed the limit.
	MaxSpanPayloadBytes int
}

// Client is an offline exporter for LLM Obs spans and evaluations. It is safe
// for concurrent use.
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

// New builds a Client from cfg.
func New(cfg Config) (*Client, error) {
	// ml_app is required for LLM Obs data; the live client rejects an empty one.
	// Requiring it here means every exported span/evaluation carries an ml_app
	// (stamped from this default, or overridden per span/metric).
	if cfg.MLApp == "" {
		return nil, errors.New("llmobs/export: MLApp is required")
	}
	site := cfg.Site
	if site == "" {
		site = defaultSite
	}
	agentless := cfg.AgentURL == ""
	if agentless && cfg.APIKey == "" {
		return nil, errors.New("llmobs/export: APIKey is required for direct Datadog export; set AgentURL to route via the Agent")
	}

	var agentURL *url.URL
	if cfg.AgentURL != "" {
		// Trim a trailing slash so the EVP proxy path is not doubled.
		u, err := url.Parse(strings.TrimRight(cfg.AgentURL, "/"))
		if err != nil {
			return nil, fmt.Errorf("llmobs/export: invalid AgentURL: %w", err)
		}
		// Require a supported scheme with a host (or unix socket path). Otherwise a
		// value like "localhost:8126" or a typo like "htt://host" parses but every
		// export fails later in Post with "unsupported protocol scheme".
		switch u.Scheme {
		case "http", "https":
			if u.Host == "" {
				return nil, fmt.Errorf("llmobs/export: invalid AgentURL %q: missing host", cfg.AgentURL)
			}
		case "unix":
			if u.Path == "" {
				return nil, fmt.Errorf("llmobs/export: invalid AgentURL %q: missing unix socket path", cfg.AgentURL)
			}
		default:
			return nil, fmt.Errorf("llmobs/export: invalid AgentURL %q: scheme must be http, https, or unix", cfg.AgentURL)
		}
		agentURL = u
	}

	icfg := &config.Config{
		ResolvedAgentlessEnabled: agentless,
		TracerConfig: config.TracerConfig{
			Site:       site,
			APIKey:     cfg.APIKey,
			AgentURL:   agentURL,
			HTTPClient: cfg.HTTPClient,
			Service:    cfg.Service,
			Env:        cfg.Env,
			Version:    cfg.Version,
		},
	}
	if icfg.TracerConfig.HTTPClient == nil {
		icfg.TracerConfig.HTTPClient = icfg.DefaultHTTPClient()
	}

	c := &Client{
		transport:    transport.New(icfg),
		service:      cfg.Service,
		env:          cfg.Env,
		version:      cfg.Version,
		mlApp:        cfg.MLApp,
		spanBatch:    orDefault(cfg.SpanBatchSize, defaultSpanBatchSize),
		evalBatch:    orDefault(cfg.EvalBatchSize, defaultEvalBatchSize),
		maxSpanBytes: orDefault(cfg.MaxSpanPayloadBytes, defaultMaxSpanBytes),
	}
	return c, nil
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// tracerVersion is the value stamped into _dd.tracer_version.
func tracerVersion() string {
	return version.Tag
}
