// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"io"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

// MetricHandle can be used to submit different values for the same metric.
// MetricHandle is used to reduce lock contention when submitting metrics.
// This can also be used ephemerally to submit a single metric value like this:
//
// ```go
// telemetry.Metric(telemetry.Appsec, "my-count", map[string]string{"tag1": "true", "tag2": "1.0"}).Submit(1.0)
// ```
type MetricHandle interface {
	Submit(value float64)

	flush()
}

// Integration is an integration that is configured to be traced.
type Integration struct {
	// Name is an arbitrary string that must stay constant for the integration.
	Name string
	// Version is the version of the integration/dependency that is being loaded.
	Version string
	// Error is the error that occurred while loading the integration. If this field is specified, the integration is
	// considered to be having been forcefully disabled because of the error.
	Error string
}

// Client constitutes all the functions available concurrently for the telemetry users. All methods are thread-safe
// This is an interface for easier testing but all functions will be mirrored at the package level to call
// the global client.
type Client interface {
	io.Closer

	// Count creates a new metric handle for the given parameters that can be used to submit values.
	Count(namespace types.Namespace, name string, tags map[string]string) MetricHandle

	// Rate creates a new metric handle for the given parameters that can be used to submit values.
	Rate(namespace types.Namespace, name string, tags map[string]string) MetricHandle

	// Gauge creates a new metric handle for the given parameters that can be used to submit values.
	Gauge(namespace types.Namespace, name string, tags map[string]string) MetricHandle

	// Distribution creates a new metric handle for the given parameters that can be used to submit values.
	Distribution(namespace types.Namespace, name string, tags map[string]string) MetricHandle

	// Log sends a telemetry log at the desired level with the given text and options.
	// Options include sending key-value pairs as tags, and a stack trace frozen from inside the Log function.
	Log(level LogLevel, text string, options ...LogOption)

	// ProductStarted declares a product to have started at the customer’s request
	ProductStarted(product types.Namespace)

	// ProductStopped declares a product to have being stopped by the customer
	ProductStopped(product types.Namespace)

	// ProductStartError declares that a product could not start because of the following error
	ProductStartError(product types.Namespace, err error)

	// AddAppConfig adds a key value pair to the app configuration and send the change to telemetry
	// value has to be json serializable and the origin is the source of the change.
	AddAppConfig(key string, value any, origin types.Origin)

	// AddBulkAppConfig adds a list of key value pairs to the app configuration and sends the change to telemetry.
	// Same as AddAppConfig but for multiple values.
	AddBulkAppConfig(kvs map[string]any, origin types.Origin)

	// MarkIntegrationAsLoaded marks an integration as loaded in the telemetry
	MarkIntegrationAsLoaded(integration Integration)

	// Flush closes the client and flushes any remaining data.
	Flush()

	// appStart sends the telemetry necessary to signal that the app is starting.
	appStart()

	// appStop sends the telemetry necessary to signal that the app is stopping.
	appStop()
}
