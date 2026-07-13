// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"net/http"
	"os"
)

// Option is a functional option for configuring the crashtracker.
type Option func(*config)

type config struct {
	service    string
	env        string
	version    string
	agentURL   string
	httpClient *http.Client
	apiKey     string
	site       string
	enabled    bool

	// pipeWriteEnd is the write end of the crash pipe registered with
	// runtime/debug.SetCrashOutput. It is stored so Stop can close it; it is
	// nil until the monitor child has been spawned.
	pipeWriteEnd *os.File
}

// WithService sets the service name tag on crash reports.
func WithService(service string) Option {
	return func(c *config) { c.service = service }
}

// WithEnv sets the env tag on crash reports.
func WithEnv(env string) Option {
	return func(c *config) { c.env = env }
}

// WithVersion sets the version tag on crash reports.
func WithVersion(version string) Option {
	return func(c *config) { c.version = version }
}

// WithAgentURL configures the Datadog Agent URL for report upload.
func WithAgentURL(rawURL string) Option {
	return func(c *config) { c.agentURL = rawURL }
}

// WithHTTPClient sets a custom HTTP client for report upload.
func WithHTTPClient(c *http.Client) Option {
	return func(cfg *config) { cfg.httpClient = c }
}

// WithAPIKey sets the Datadog API key for agentless upload.
func WithAPIKey(apiKey string) Option {
	return func(c *config) { c.apiKey = apiKey }
}

// WithSite sets the Datadog site for agentless intake (e.g. "datadoghq.com").
func WithSite(site string) Option {
	return func(c *config) { c.site = site }
}

// WithEnabled explicitly enables or disables the crashtracker, overriding the
// DD_CRASHTRACKING_ENABLED environment gate. When disabled, Start does not spawn
// the monitor process and returns nil.
func WithEnabled(enabled bool) Option {
	return func(c *config) { c.enabled = enabled }
}
