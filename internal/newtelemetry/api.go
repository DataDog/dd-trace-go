// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"net/http"
)

type ClientConfig struct {
	// AgentlessURL is the full URL to the agentless telemetry endpoint.
	// Defaults to https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry
	AgentlessURL string

	// AgentURL is the url to the agent without the path
	AgentURL string

	// APIKey is the API key to use for sending telemetry, defaults to the env var DD_API_KEY.
	APIKey string

	// HTTPClient is the http client to use for sending telemetry, defaults to http.DefaultClient.
	HTTPClient http.RoundTripper
}

// NewClient creates a new telemetry client with the given service, environment, and version and config.
func NewClient(service, env, version string, config ClientConfig) (Client, error) {
	return nil, nil
}

// StartApp starts the telemetry client with the given client send the app-started telemetry and sets it as the global client.
func StartApp(client Client) error {
	return nil
}

// StopApp creates the app-stopped telemetry, adding to the queue and flush all the queue before stopping the client.
func StopApp() {
}

// MetricHandle is used to reduce lock contention when submitting metrics.
// This can also be used ephemerally to submit a single metric value like this:
//
//	telemetry.Metric(telemetry.Appsec, "my-count").Submit(1.0, []string{"tag1:true", "tag2:1.0"})
type MetricHandle interface {
	Submit(value float64, tags []string)

	flush()
}

// Logger is the interface i
type Logger interface {
	// WithTags creates a new Logger which will send a comma-separated list of tags with the next logs
	WithTags(tags string) Logger

	// WithStackTrace creates a new Logger which will send a stack trace generated for each next log.
	WithStackTrace(tags string) Logger

	// Error sends a telemetry log at the ERROR level
	Error(text string)

	// Warn sends a telemetry log at the WARN level
	Warn(text string)

	// Debug sends a telemetry log at the DEBUG level
	Debug(text string)
}

// Client constitutes all the functions available concurrently for the telemetry users.
type Client interface {
	// Count creates a new metric handle for the given namespace and name that can be used to submit values.
	Count(namespace Namespace, name string) MetricHandle

	// Rate creates a new metric handle for the given namespace and name that can be used to submit values.
	Rate(namespace Namespace, name string) MetricHandle

	// Gauge creates a new metric handle for the given namespace and name that can be used to submit values.
	Gauge(namespace Namespace, name string) MetricHandle

	// Distribution creates a new metric handle for the given namespace and name that can be used to submit values.
	Distribution(namespace Namespace, name string) MetricHandle

	// Logger returns an implementation of the Logger interface which sends telemetry logs.
	Logger() Logger

	// ProductOnOff sent the telemetry necessary to signal that a product is enabled/disabled.
	ProductOnOff(product Namespace, enabled bool)

	// AddAppConfig adds a key value pair to the app configuration and send the change to telemetry
	// value has to be json serializable and the origin is the source of the change.
	AddAppConfig(key string, value any, origin Origin)

	// AddBulkAppConfig adds a list of key value pairs to the app configuration and send the change to telemetry.
	// Same as AddAppConfig but for multiple values.
	AddBulkAppConfig(kvs []Configuration)

	// flush closes the client and flushes any remaining data.
	flush()

	// appStart sends the telemetry necessary to signal that the app is starting.
	appStart()

	// appStop sends the telemetry necessary to signal that the app is stopping and calls Close()
	appStop()
}
