// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"sync/atomic"

	globalinternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/types"
)

var (
	globalClient atomic.Pointer[Client]

	// globalClientRecorder contains all actions done on the global client done before StartApp() with an actual client object is called
	globalClientRecorder = internal.NewRecorder[Client]()
)

// StartApp starts the telemetry client with the given client send the app-started telemetry and sets it as the global (*client).
func StartApp(client Client) {
	if Disabled() || globalClient.Load() != nil {
		return
	}

	SwapClient(client)

	globalClientRecorder.Replay(client)

	client.appStart()
}

// SwapClient swaps the global client with the given client and Flush the old (*client).
func SwapClient(client Client) {
	if Disabled() {
		return
	}

	if oldClient := globalClient.Swap(&client); oldClient != nil && *oldClient != nil {
		(*oldClient).Close()
	}
}

// StopApp creates the app-stopped telemetry, adding to the queue and Flush all the queue before stopping the (*client).
func StopApp() {
	if Disabled() || globalClient.Load() == nil {
		return
	}

	if client := globalClient.Swap(nil); client != nil && *client != nil {
		(*client).appStop()
		(*client).Flush()
		(*client).Close()
	}
}

// Disabled returns whether instrumentation telemetry is disabled
// according to the DD_INSTRUMENTATION_TELEMETRY_ENABLED env var
func Disabled() bool {
	return !globalinternal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true)
}

// Count creates a new metric handle for the given parameters that can be used to submit values.
func Count(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if Disabled() {
		return nil
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		return nil
	}
	return (*client).Count(namespace, name, tags)
}

// Rate creates a new metric handle for the given parameters that can be used to submit values.
func Rate(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if Disabled() {
		return nil
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		return nil
	}
	return (*client).Rate(namespace, name, tags)
}

// Gauge creates a new metric handle for the given parameters that can be used to submit values.
func Gauge(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if Disabled() {
		return nil
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		return nil
	}
	return (*client).Gauge(namespace, name, tags)
}

// Distribution creates a new metric handle for the given parameters that can be used to submit values.
func Distribution(namespace types.Namespace, name string, tags map[string]string) MetricHandle {
	if Disabled() {
		return nil
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		return nil
	}
	return (*client).Distribution(namespace, name, tags)
}

func Log(level LogLevel, text string, options ...LogOption) {
	globalClientCall(func(client Client) {
		client.Log(level, text, options...)
	})
}

// ProductStarted declares a product to have started at the customer’s request. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStarted(product types.Namespace) {
	globalClientCall(func(client Client) {
		client.ProductStarted(product)
	})
}

// ProductStopped declares a product to have being stopped by the customer. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStopped(product types.Namespace) {
	globalClientCall(func(client Client) {
		client.ProductStopped(product)
	})
}

// ProductStartError declares that a product could not start because of the following error. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStartError(product types.Namespace, err error) {
	globalClientCall(func(client Client) {
		client.ProductStartError(product, err)
	})
}

// AddAppConfig adds a key value pair to the app configuration and send the change to telemetry
// value has to be json serializable and the origin is the source of the change. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func AddAppConfig(key string, value any, origin types.Origin) {
	globalClientCall(func(client Client) {
		client.AddAppConfig(key, value, origin)
	})
}

// AddBulkAppConfig adds a list of key value pairs to the app configuration and sends the change to telemetry.
// Same as AddAppConfig but for multiple values. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func AddBulkAppConfig(kvs map[string]any, origin types.Origin) {
	globalClientCall(func(client Client) {
		client.AddBulkAppConfig(kvs, origin)
	})
}

// globalClientCall takes a function that takes a Client and calls it with the global client if it exists.
// otherwise, it records the action for when the client is started.
func globalClientCall(fun func(client Client)) {
	if Disabled() {
		return
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		globalClientRecorder.Record(func(client Client) {
			fun(client)
		})
		return
	}

	fun(*client)
}

type metricsHotPointer struct {
	ptr      atomic.Pointer[MetricHandle]
	recorder internal.Recorder[MetricHandle]
}

func (t *metricsHotPointer) Submit(value float64) {
	inner := t.ptr.Load()
	if inner == nil || *inner == nil {
		t.recorder.Record(func(handle MetricHandle) {
			handle.Submit(value)
		})
	}

	(*inner).Submit(value)
}

func (t *metricsHotPointer) flush() {
	inner := t.ptr.Load()
	if inner == nil || *inner == nil {
		return
	}

	(*inner).flush()
}

func (t *metricsHotPointer) swap(handle MetricHandle) {
	if t.ptr.Swap(&handle) == nil {
		t.recorder.Replay(handle)
	}
}

var _ MetricHandle = (*metricsHotPointer)(nil)
