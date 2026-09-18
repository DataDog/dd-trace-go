// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"net/http"
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

	// agentlessURL is a test-only override for the agentless upload target.
	// There is no public option for it: WithAgentURL always means the agent,
	// so combining WithAgentURL and WithAPIKey cannot silently drop the report
	// by pointing the agentless path at a pathless host (see upload.go).
	agentlessURL string

	// foreignThreadSignals is set by WithForeignThreadSignals.
	foreignThreadSignals bool
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

// WithAPIKey sets the Datadog API key for agentless upload. The key reaches
// the monitor child process through its environment (like every other option;
// see buildChildEnv), so choosing WithAPIKey over DD_API_KEY does not keep the
// key out of a process environment — it only keeps it out of the
// application's own.
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

// WithForeignThreadSignals opts into best-effort visibility for a fault on a
// thread created entirely by native code — a pthread spawned by a C library
// that never entered Go. runtime/debug.SetCrashOutput cannot observe this at
// all (see doc.go's cgo section): with no saved native handler and no signal
// notification requested, the process terminates with no Go crash report of
// any kind. Off by default.
//
// This is not crash recovery and not a full report. There is no stack or
// register context for a thread the Go runtime never tracked, so the report
// carries only the signal name and a timestamp. It also does not resolve the
// underlying fault: the instruction that faulted is still invalid, and
// retrying it (which is what returning from a signal handler does) will
// fault again — verified to loop indefinitely across multiple CPU cores if
// nothing intervenes. The first occurrence reports before resetting the
// signal to its default disposition (see [signal.Reset]): resetting first
// was measured to leave under 200 microseconds before the process exits on
// the near-certain next occurrence, far too short for any upload to
// complete. Reporting first instead means the fault keeps retrying in the
// background for the length of one upload attempt — a bounded CPU cost, in
// exchange for a report that can actually be delivered — after which reset
// lets that next occurrence terminate the process normally.
func WithForeignThreadSignals(enabled bool) Option {
	return func(c *config) { c.foreignThreadSignals = enabled }
}
