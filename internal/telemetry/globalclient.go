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

// telemetryQueuedLogStackSkip skips Log's own frame — landing on Log's
// caller, the actual site that requested a stacktrace before the global
// client existed. CaptureRaw already accounts for its own frame and
// runtime.Callers internally.
const telemetryQueuedLogStackSkip = 1

type startAppAttempt struct {
	generation uint64
	flushes    *sync.WaitGroup
}

func (a startAppAttempt) done() {
	a.flushes.Done()
}

var (
	globalClient atomic.Pointer[Client]

	// globalClientRecorder contains all actions done on the global client done before StartApp() with an actual client object is called
	globalClientRecorder = internal.NewRecorder[Client]()

	// metricsHandleSwappablePointers contains all the swappableMetricHandle, used to replay actions done before the actual MetricHandle is set
	metricsHandleSwappablePointers = xsync.NewMap[metricKey, *swappableMetricHandle](xsync.WithPresize(knownmetrics.Size()))

	// globalClientLifecycle serializes preparation, publication, and removal.
	// Its generation advances whenever a shutdown/removal publishes an empty
	// slot, so a start prepared against an older empty slot cannot publish after
	// that shutdown. Each generation has its own WaitGroup so a shutdown waits
	// only for attempts admitted before it advanced the generation.
	globalClientLifecycle = struct {
		sync.Mutex
		generation      uint64
		startAppFlushes *sync.WaitGroup
	}{startAppFlushes: new(sync.WaitGroup)}
)

// GlobalClient returns the global telemetry client.
func GlobalClient() Client {
	client := globalClient.Load()
	if client == nil {
		return nil
	}
	return *client
}

// asClient returns the *client behind c, or nil when c is another Client
// implementation such as a test double.
func asClient(c Client) *client {
	if cc, ok := c.(*client); ok {
		return cc
	}
	return nil
}

// StartApp starts the telemetry client with the given client send the app-started telemetry and sets it as the global (*client)
// then calls client.Flush on the client asynchronously.
func StartApp(client Client) {
	if Disabled() {
		return
	}

	attempt, empty := prepareStartApp()
	if !empty {
		log.Debug("telemetry: StartApp called multiple times, ignoring")
		return
	}

	finishOwnsRegistration := false
	defer func() {
		if !finishOwnsRegistration {
			attempt.done()
		}
	}()

	client.AppStart()
	c := asClient(client)
	var done chan struct{}
	if c != nil {
		done = c.markStartFlushPending()
		if done == nil {
			log.Debug("telemetry: StartApp called multiple times, ignoring")
			return
		}
	}

	finishOwnsRegistration = true
	if !finishStartApp(client, c, done, attempt) {
		log.Debug("telemetry: StartApp called multiple times, ignoring")
	}
}

// prepareStartApp admits a start only while the global slot is empty. It
// registers the attempt before releasing the lifecycle lock, so a shutdown
// that removes a concurrently published winner cannot begin waiting before a
// delayed contender is included.
func prepareStartApp() (startAppAttempt, bool) {
	globalClientLifecycle.Lock()
	defer globalClientLifecycle.Unlock()
	if GlobalClient() != nil {
		return startAppAttempt{}, false
	}
	attempt := startAppAttempt{
		generation: globalClientLifecycle.generation,
		flushes:    globalClientLifecycle.startAppFlushes,
	}
	attempt.flushes.Add(1)
	return attempt, true
}

// finishStartApp publishes and flushes a prepared start attempt, or cleans it
// up when another client or a newer lifecycle generation won. Once called, it
// owns the attempt's WaitGroup registration on every return and panic path.
func finishStartApp(client Client, c *client, done chan struct{}, attempt startAppAttempt) bool {
	handedOff := false
	defer func() {
		if !handedOff {
			attempt.done()
		}
	}()

	if !installClientIfEmpty(client, c, done, attempt.generation) {
		completeLosingStartFlush(c, done)
		return false
	}

	handedOff = true
	go func() {
		defer attempt.done()
		if c != nil {
			defer c.completeStartFlush(done)
		}
		client.Flush()
	}()
	return true
}

// installClientIfEmpty installs and activates client only when the global slot
// is still empty in generation. Publication and lifecycle changes are
// serialized, while activation remains outside the lifecycle lock.
func installClientIfEmpty(client Client, c *client, done chan struct{}, generation uint64) bool {
	globalClientLifecycle.Lock()
	if globalClientLifecycle.generation != generation || GlobalClient() != nil {
		globalClientLifecycle.Unlock()
		return false
	}
	if c != nil && !c.claimStartFlush(done) {
		globalClientLifecycle.Unlock()
		return false
	}
	globalClient.Store(&client)
	globalClientLifecycle.Unlock()

	activateClient(client)
	return true
}

func activateClient(client Client) {
	globalClientRecorder.Replay(client)
	// Swap all metrics hot pointers to the new MetricHandle.
	metricsHandleSwappablePointers.Range(func(_ metricKey, value *swappableMetricHandle) bool {
		value.swap(value.maker(client))
		return true
	})
}

// completeLosingStartFlush completes an unused marker. A marker claimed by a
// same-client winner remains pending for the winning flush.
func completeLosingStartFlush(c *client, done chan struct{}) {
	if c != nil {
		c.completeUnclaimedStartFlush(done)
	}
}

// advanceGlobalClientLifecycle starts a fresh generation and returns the
// WaitGroup containing attempts admitted to the previous one. The caller must
// hold globalClientLifecycle.
func advanceGlobalClientLifecycle() *sync.WaitGroup {
	flushes := globalClientLifecycle.startAppFlushes
	globalClientLifecycle.generation++
	globalClientLifecycle.startAppFlushes = new(sync.WaitGroup)
	return flushes
}

// clearGlobalClient removes the global client when it still is c, and
// returns the removed client. It returns nil when no global client is set or
// another client replaced c. The caller must close the returned client: this
// function does not close it, because the caller may run on one of the
// client's own goroutines, and Close joins those goroutines.
func clearGlobalClient(c Client) Client {
	globalClientLifecycle.Lock()
	defer globalClientLifecycle.Unlock()
	cur := GlobalClient()
	if cur == nil || cur != c {
		return nil
	}
	advanceGlobalClientLifecycle()
	globalClient.Store(nil)
	return cur
}

// SwapClient swaps the global client with the given client and Flush the old (*client).
func SwapClient(client Client) Client {
	if Disabled() {
		return nil
	}

	globalClientLifecycle.Lock()
	oldClient := GlobalClient()
	if client == nil {
		advanceGlobalClientLifecycle()
		globalClient.Store(nil)
	} else {
		globalClient.Store(&client)
	}
	globalClientLifecycle.Unlock()

	if oldClient != nil {
		oldClient.Close()
	}

	if client == nil {
		return oldClient
	}

	activateClient(client)
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
	globalClientLifecycle.Lock()
	flushes := advanceGlobalClientLifecycle()
	client := GlobalClient()
	globalClient.Store(nil)
	globalClientLifecycle.Unlock()

	if client != nil {
		client.AppStop()
		flushes.Wait()
		client.Flush()
		client.Close()
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
