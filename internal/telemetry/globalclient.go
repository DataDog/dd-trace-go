// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package telemetry

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/puzpuzpuz/xsync/v4"

	globalinternal "github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/stacktrace"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/knownmetrics"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// telemetryQueuedLogStackSkip skips: runtime.Callers, CaptureRaw, Log's own
// frame — landing on Log's caller, the actual site that requested a
// stacktrace before the global client existed.
const telemetryQueuedLogStackSkip = 3

var (
	globalClient atomic.Pointer[Client]

	// globalClientRecorder contains all actions done on the global client done before StartApp() with an actual client object is called
	globalClientRecorder = internal.NewRecorder[Client]()

	// metricsHandleSwappablePointers contains all the swappableMetricHandle, used to replay actions done before the actual MetricHandle is set
	metricsHandleSwappablePointers = xsync.NewMap[metricKey, *swappableMetricHandle](xsync.WithPresize(knownmetrics.Size()))

	// startAppFlushWg tracks the goroutine launched by StartApp so StopApp can
	// wait for it to finish before proceeding with the shutdown flush.
	startAppFlushWg sync.WaitGroup
)

// GlobalClient returns the global telemetry client.
func GlobalClient() Client {
	client := globalClient.Load()
	if client == nil {
		return nil
	}
	return *client
}

// StartApp starts the telemetry client with the given client send the app-started telemetry and sets it as the global (*client)
// then calls client.Flush on the client asynchronously.
func StartApp(client Client) {
	if Disabled() {
		return
	}

	if GlobalClient() != nil {
		log.Debug("telemetry: StartApp called multiple times, ignoring")
		return
	}

	client.AppStart()
	// Increment the WaitGroup before SwapClient makes the client visible so
	// StopApp cannot observe a zero counter and return before the flush goroutine runs.
	startAppFlushWg.Add(1)
	if SwapClient(client) != nil {
		// A concurrent StartApp call already set the client; undo the Add.
		startAppFlushWg.Done()
		log.Debug("telemetry: StartApp called multiple times, ignoring")
		return
	}

	go func() {
		defer startAppFlushWg.Done()
		client.Flush()
	}()
}

// SwapClient swaps the global client with the given client and Flush the old (*client).
func SwapClient(client Client) Client {
	if Disabled() {
		return nil
	}

	oldClientPtr := globalClient.Swap(&client)
	var oldClient Client
	if oldClientPtr != nil && *oldClientPtr != nil {
		oldClient = *oldClientPtr
	}

	if oldClient != nil {
		oldClient.Close()
	}

	if client == nil {
		return oldClient
	}

	globalClientRecorder.Replay(client)
	// Swap all metrics hot pointers to the new MetricHandle
	metricsHandleSwappablePointers.Range(func(_ metricKey, value *swappableMetricHandle) bool {
		value.swap(value.maker(client))
		return true
	})

	return oldClient
}

// MockClient swaps the global client with the given client and clears the recorder to make sure external calls are not replayed.
// It returns a function that can be used to swap back the global client
func MockClient(client Client) func() {
	globalClientRecorder.Clear()
	metricsHandleSwappablePointers.Clear()

	oldClient := SwapClient(client)
	return func() {
		SwapClient(oldClient)
	}
}

// StopApp creates the app-stopped telemetry, adding to the queue and Flush all the queue before stopping the (*client).
func StopApp() {
	if client := globalClient.Swap(nil); client != nil && *client != nil {
		(*client).AppStop()
		startAppFlushWg.Wait()
		(*client).Flush()
		(*client).Close()
	}
}

var (
	telemetryClientEnabled bool
	telemetryEnabledOnce   sync.Once
)

// Disabled returns whether instrumentation telemetry is disabled
// according to the DD_INSTRUMENTATION_TELEMETRY_ENABLED env var
func Disabled() bool {
	telemetryEnabledOnce.Do(func() {
		telemetryClientEnabled = globalinternal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true)
	})
	return telemetryClientEnabled == false
}

// Count creates a new metric handle for the given parameters that can be used to submit values.
// Count will always return a [MetricHandle], even if telemetry is disabled or the client has yet to start.
// The [MetricHandle] is then swapped with the actual [MetricHandle] once the client is started.
func Count(namespace Namespace, name string, tags []string) MetricHandle {
	return globalClientNewMetric(namespace, transport.CountMetric, name, tags)
}

// Rate creates a new metric handle for the given parameters that can be used to submit values.
// Rate will always return a [MetricHandle], even if telemetry is disabled or the client has yet to start.
// The [MetricHandle] is then swapped with the actual [MetricHandle] once the client is started.
func Rate(namespace Namespace, name string, tags []string) MetricHandle {
	return globalClientNewMetric(namespace, transport.RateMetric, name, tags)
}

// Gauge creates a new metric handle for the given parameters that can be used to submit values.
// Gauge will always return a [MetricHandle], even if telemetry is disabled or the client has yet to start.
// The [MetricHandle] is then swapped with the actual [MetricHandle] once the client is started.
func Gauge(namespace Namespace, name string, tags []string) MetricHandle {
	return globalClientNewMetric(namespace, transport.GaugeMetric, name, tags)
}

// Distribution creates a new metric handle for the given parameters that can be used to submit values.
// Distribution will always return a [MetricHandle], even if telemetry is disabled or the client has yet to start.
// The [MetricHandle] is then swapped with the actual [MetricHandle] once the client is started.
// The Get() method of the [MetricHandle] will return the last value submitted.
// Distribution MetricHandle is advised to be held in a variable more than the rest of the metric types to avoid too many useless allocations.
func Distribution(namespace Namespace, name string, tags []string) MetricHandle {
	return globalClientNewMetric(namespace, transport.DistMetric, name, tags)
}

func Log(record Record, options ...LogOption) {
	// If the global client isn't installed yet, this call is about to be
	// queued and replayed later on a different goroutine (see
	// globalClientCall). A stacktrace captured at replay time would belong
	// to that goroutine, not this call site — so capture it now, before
	// queuing, whenever one was requested. This runs on every pre-StartApp
	// call requesting a stacktrace, even ones that will end up deduplicated
	// away; that's an acceptable cost given how rare and low-volume the
	// pre-StartApp window is.
	//
	// Disabled() is checked first: when telemetry is off, StartApp never
	// installs a client, so GlobalClient() is permanently nil and this
	// branch would otherwise capture a stack on every single call — on a
	// hot, repeatedly-invoked path (e.g. AppSec's exception recording) —
	// only for globalClientCall to discard it a line later via its own
	// Disabled() check.
	if !Disabled() && GlobalClient() == nil && wantsStacktrace(options) {
		raw := stacktrace.CaptureRaw(telemetryQueuedLogStackSkip)
		options = append(slices.Clone(options), withRawStacktrace(raw))
	}
	globalClientCall(func(client Client) {
		client.Log(record, options...)
	})
}

// ProductStarted declares a product to have started at the customer’s request. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStarted(product Namespace) {
	globalClientCall(func(client Client) {
		client.ProductStarted(product)
	})
}

// ProductStopped declares a product to have being stopped by the customer. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStopped(product Namespace) {
	globalClientCall(func(client Client) {
		client.ProductStopped(product)
	})
}

// ProductStartError declares that a product could not start because of the following error. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func ProductStartError(product Namespace, err error) {
	globalClientCall(func(client Client) {
		client.ProductStartError(product, err)
	})
}

// RegisterAppConfig adds a key value pair to the app configuration and send the change to telemetry
// value has to be json serializable and the origin is the source of the change. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func RegisterAppConfig(key string, value any, origin Origin) {
	globalClientCall(func(client Client) {
		client.RegisterAppConfig(key, value, origin)
	})
}

// RegisterAppConfigs adds a list of key value pairs to the app configuration and sends the change to telemetry.
// Same as AddAppConfig but for multiple values. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func RegisterAppConfigs(kvs ...Configuration) {
	globalClientCall(func(client Client) {
		client.RegisterAppConfigs(kvs...)
	})
}

// RegisterAppEndpoint reports a new REST endpoint exposed by the application.
// This can be called multiple times and endpoints will be accumulated
// additively by the backend.
func RegisterAppEndpoint(opName string, resName string, attrs AppEndpointAttributes) {
	globalClientCall(func(client Client) {
		client.RegisterAppEndpoint(opName, resName, attrs)
	})
}

// MarkIntegrationAsLoaded marks an integration as loaded in the telemetry. If telemetry is disabled
// or the client has not started yet it will record the action and replay it once the client is started.
func MarkIntegrationAsLoaded(integration Integration) {
	globalClientCall(func(client Client) {
		client.MarkIntegrationAsLoaded(integration)
	})
}

// LoadIntegration marks an integration as loaded in the telemetry client. If telemetry is disabled, it will do nothing.
// If the telemetry client has not started yet, it will record the action and replay it once the client is started.
func LoadIntegration(integration string) {
	globalClientCall(func(client Client) {
		client.MarkIntegrationAsLoaded(Integration{
			Name: integration,
		})
	})
}

// AddFlushTicker adds a function that is called at each telemetry Flush. By default, every minute
func AddFlushTicker(ticker func(Client)) {
	globalClientCall(func(client Client) {
		client.AddFlushTicker(ticker)
	})
}

var globalClientLogLossOnce sync.Once

// globalClientCall takes a function that takes a Client and calls it with the global client if it exists.
// otherwise, it records the action for when the client is started.
func globalClientCall(fun func(client Client)) {
	if Disabled() {
		return
	}

	client := globalClient.Load()
	if client == nil || *client == nil {
		if !globalClientRecorder.Record(fun) {
			globalClientLogLossOnce.Do(func() {
				log.Debug("telemetry: global client recorder queue is full, dropping telemetry data, please start the telemetry client earlier to avoid data loss")
			})
		}
		return
	}

	fun(*client)
}

var noopMetricHandleInstance = noopMetricHandle{}

func globalClientNewMetric(namespace Namespace, kind transport.MetricType, name string, tags []string) MetricHandle {
	if Disabled() {
		return noopMetricHandleInstance
	}

	key := newMetricKey(namespace, kind, name, tags)
	hotPtr, _ := metricsHandleSwappablePointers.LoadOrCompute(key, func() (*swappableMetricHandle, bool) {
		maker := func(client Client) MetricHandle {
			switch kind {
			case transport.CountMetric:
				return client.Count(namespace, name, tags)
			case transport.RateMetric:
				return client.Rate(namespace, name, tags)
			case transport.GaugeMetric:
				return client.Gauge(namespace, name, tags)
			case transport.DistMetric:
				return client.Distribution(namespace, name, tags)
			}
			log.Warn("telemetry: unknown metric type %q", kind)
			return nil
		}
		wrapper := &swappableMetricHandle{maker: maker}
		if client := globalClient.Load(); client == nil || *client == nil {
			wrapper.recorder = internal.NewRecorder[MetricHandle]()
		}
		globalClientCall(func(client Client) {
			wrapper.swap(maker(client))
		})
		return wrapper, false
	})
	return hotPtr
}
