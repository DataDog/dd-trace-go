// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"sync/atomic"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

var (
	globalClient atomic.Pointer[Client]
)

// StartApp starts the telemetry client with the given client send the app-started telemetry and sets it as the global (*client).
func StartApp(client Client) error {
	client.appStart()
	SwapClient(client)
	return nil
}

// SwapClient swaps the global client with the given client and Flush the old (*client).
func SwapClient(client Client) {
	if oldClient := globalClient.Swap(&client); oldClient != nil && *oldClient != nil {
		(*oldClient).Close()
	}
}

// StopApp creates the app-stopped telemetry, adding to the queue and Flush all the queue before stopping the (*client).
func StopApp() {
	if client := globalClient.Swap(nil); client != nil && *client != nil {
		(*client).appStop()
	}
}

func Count(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if client := globalClient.Load(); client != nil && *client != nil {
		return (*client).Count(namespace, name, tags)
	}

	return nil
}

// Rate creates a new metric handle for the given parameters that can be used to submit values.
func Rate(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if client := globalClient.Load(); client != nil && *client != nil {
		return (*client).Rate(namespace, name, tags)
	}

	return nil
}

// Gauge creates a new metric handle for the given parameters that can be used to submit values.
func Gauge(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if client := globalClient.Load(); client != nil && *client != nil {
		return (*client).Gauge(namespace, name, tags)
	}

	return nil
}

// Distribution creates a new metric handle for the given parameters that can be used to submit values.
func Distribution(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if client := globalClient.Load(); client != nil && *client != nil {
		return (*client).Distribution(namespace, name, tags)
	}

	return nil
}

// Logger returns an implementation of the TelemetryLogger interface which sends telemetry logs.
func Logger() TelemetryLogger {
	if client := globalClient.Load(); client != nil && *client != nil {
		return (*client).Logger()
	}

	return nil
}

// ProductStarted declares a product to have started at the customer’s request
func ProductStarted(product types.Namespace) {
	if client := globalClient.Load(); client != nil && *client != nil {
		(*client).ProductStarted(product)
	}
}

// ProductStopped declares a product to have being stopped by the customer
func ProductStopped(product types.Namespace) {
	if client := globalClient.Load(); client != nil && *client != nil {
		(*client).ProductStopped(product)
	}
}

// ProductStartError declares that a product could not start because of the following error
func ProductStartError(product types.Namespace, err error) {
	if client := globalClient.Load(); client != nil && *client != nil {
		(*client).ProductStartError(product, err)
	}
}

// AddAppConfig adds a key value pair to the app configuration and send the change to telemetry
// value has to be json serializable and the origin is the source of the change.
func AddAppConfig(key string, value any, origin types.Origin) {
	if client := globalClient.Load(); client != nil && *client != nil {
		(*client).AddAppConfig(key, value, origin)
	}
}

// AddBulkAppConfig adds a list of key value pairs to the app configuration and sends the change to telemetry.
// Same as AddAppConfig but for multiple values.
func AddBulkAppConfig(kvs map[string]any, origin types.Origin) {
	if client := globalClient.Load(); client != nil && *client != nil {
		(*client).AddBulkAppConfig(kvs, origin)
	}
}
