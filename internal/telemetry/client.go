// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

// Package telemetry implements a client for sending telemetry information to
// Datadog regarding usage of an APM library such as tracing or profiling.
package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

var (
	// GlobalClient acts as a global telemetry client that the
	// tracer, profiler, and appsec products will use
	GlobalClient *Client
	// copied from dd-trace-go/profiler
	defaultHTTPClient = &http.Client{
		// We copy the transport to avoid using the default one, as it might be
		// augmented with tracing and we don't want these calls to be recorded.
		// See https://golang.org/pkg/net/http/#DefaultTransport .
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 5 * time.Second,
	}
	// TODO: Default telemetry URL?
	hostname string

	// protects agentlessURL, which may be changed for testing purposes
	agentlessEndpointLock sync.RWMutex

	agentlessURL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"

	defaultHeartbeatInterval = 60 // seconds

	// LogPrefix specifies the prefix for all telemetry logging
	LogPrefix = "instrumentation telemetry: "
)

func init() {
	h, err := os.Hostname()
	if err == nil {
		hostname = h
	}
}

// Client buffers and sends telemetry messages to Datadog (possibly through an
// agent). Client.Start should be called before any other methods.
//
// Client is safe to use from multiple goroutines concurrently. The client will
// send all telemetry requests in the background, in order to avoid blocking the
// caller since telemetry should not disrupt an application. Metrics are
// aggregated by the Client.
type Client struct {
	// URL for the Datadog agent or Datadog telemetry endpoint
	URL string
	// APIKey should be supplied if the endpoint is not a Datadog agent,
	// i.e. you are sending telemetry directly to Datadog
	APIKey string
	// How often to send batched requests. Defaults to 60s
	SubmissionInterval time.Duration

	// The interval for sending a heartbeat signal to the backend.
	// Configurable with DD_TELEMETRY_HEARTBEAT_INTERVAL. Default 60s.
	heartbeatInterval time.Duration

	// e.g. "tracers", "profilers", "appsec"
	Namespace Namespace

	// App-specific information
	Service string
	Env     string
	Version string

	// Determines whether telemetry should actually run.
	// Defaults to true, but will be overridden by the environment variable
	// DD_INSTRUMENTATION_TELEMETRY_ENABLED is set to 0 or false
	Disabled bool

	// Determines whether dependencies should be collected
	// Defaults to true, but will be overridden by the environment variable
	// DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED is set to 0 or false
	CollectDependencies bool

	// debug enables the debug flag for all requests, see
	// https://dtdg.co/3bv2MMv If set, the DD_INSTRUMENTATION_TELEMETRY_DEBUG
	// takes precedence over this field.
	debug bool

	// Client will be used for telemetry uploads. This http.Client, if
	// provided, should be the same as would be used for any other
	// interaction with the Datadog agent, e.g. if the agent is accessed
	// over UDS, or if the user provides their own http.Client to the
	// profiler/tracer to access the agent over a proxy.
	//
	// If Client is nil, an http.Client with the same Transport settings as
	// http.DefaultTransport and a 5 second timeout will be used.
	Client *http.Client

	// mu guards all of the following fields
	mu sync.Mutex
	// started is true in between when Start() returns and the next call to
	// Stop()
	started bool
	// seqID is a sequence number used to order telemetry messages by
	// the back end.
	seqID int64
	// flushT is used to schedule flushing outstanding messages
	flushT *time.Timer
	// heartbeatT is used to schedule heartbeat messages
	heartbeatT *time.Timer
	// requests hold all messages which don't need to be immediately sent
	requests []*Request
	// metrics holds un-sent metrics that will be aggregated the next time
	// metrics are sent
	metrics    map[string]*metric
	newMetrics bool

	// logLock gaurds the logging field
	logLock sync.RWMutex
	// Logging allows us to toggle on agentless in the case where
	// there are issues with sending telemetry to the agent
	Logging bool

	// tracks tracer start errors
	Errors []Error
}

func (c *Client) log(msg string, args ...interface{}) {
	// we don't log if the client is temporarily using agentless
	// to avoid spamming the user with instrumentation telemetry error messages
	c.logLock.RLock()
	defer c.logLock.RUnlock()
	if !c.Logging {
		return
	}
	log.Warn(fmt.Sprintf(LogPrefix+msg, args...))
}

// Start registers that the app has begun running with the given integrations
// and configuration.
func (c *Client) Start(configuration []Configuration, errors []Error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Disabled || !internal.BoolEnv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", true) {
		return
	}
	if c.started {
		return
	}
	c.started = true
	c.Errors = errors
	// XXX: Should we let metrics persist between starting and stopping?
	c.metrics = make(map[string]*metric)
	c.applyDefaultOps()

	payload := &AppStarted{
		Configuration: append([]Configuration{}, configuration...),
		Products: Products{
			AppSec: ProductDetails{
				Version: version.Tag,
				Enabled: appsec.Enabled(),
			},
		},
	}

	appStarted := c.newRequest(RequestTypeAppStarted)
	appStarted.Body.Payload = payload
	c.scheduleSubmit(appStarted)

	if c.CollectDependencies || internal.BoolEnv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", true) {
		depPayload := Dependencies{[]Dependency{}}
		deps, ok := debug.ReadBuildInfo()
		if ok {
			for _, dep := range deps.Deps {
				depPayload.Dependencies = append(depPayload.Dependencies,
					Dependency{
						Name:    dep.Path,
						Version: dep.Version,
					},
				)
			}
		}
		dep := c.newRequest(RequestTypeDependenciesLoaded)
		dep.Body.Payload = depPayload
		c.scheduleSubmit(dep)
	}

	c.flush()

	if c.SubmissionInterval == 0 {
		c.SubmissionInterval = 60 * time.Second
	}
	c.flushT = time.AfterFunc(c.SubmissionInterval, c.backgroundFlush)

	heartbeat := internal.IntEnv("DD_TELEMETRY_HEARTBEAT_INTERVAL", defaultHeartbeatInterval)
	if heartbeat < 1 || heartbeat > 3600 {
		c.log("DD_TELEMETRY_HEARTBEAT_INTERVAL=%d not in [1,3600] range, setting to default of %d", heartbeat, defaultHeartbeatInterval)
		heartbeat = defaultHeartbeatInterval
	}
	c.heartbeatInterval = time.Duration(heartbeat) * time.Second
	c.heartbeatT = time.AfterFunc(c.heartbeatInterval, c.backgroundHeartbeat)
}

// Stop notifies the telemetry endpoint that the app is closing. All outstanding
// messages will also be sent. No further messages will be sent until the client
// is started again
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.started = false
	c.flushT.Stop()
	c.heartbeatT.Stop()
	// close request types have no body
	r := c.newRequest(RequestTypeAppClosing)
	c.scheduleSubmit(r)
	c.flush()
}

// ProductEnabled sends app-product-change event that signals a product has been turned on/off.
// the caller can also specify additional configuration changes (e.g. profiler config info),
// which will be sent via the app-client-configuration-change event
func (c *Client) ProductEnabled(namespace Namespace, enabled bool, configuration []Configuration) {
	productReq := c.newRequest(RequestTypeAppProductChange)
	products := new(Products)
	if namespace == NamespaceProfilers {
		products.Profiler = ProductDetails{Enabled: enabled}
	} else if namespace == NamespaceASM {
		products.AppSec = ProductDetails{Enabled: enabled}
	}
	productReq.Body.Payload = products
	c.newRequest(RequestTypeAppClientConfigurationChange)
	if len(configuration) > 0 {
		configReq := c.newRequest(RequestTypeAppClientConfigurationChange)
		configChange := new(ConfigurationChange)
		configChange.Configuration = append([]Configuration{}, configuration...)
		configReq.Body.Payload = configChange
		go func() {
			configReq.submit()
		}()
	}
	go func() {
		productReq.submit()
	}()

}

type metricKind string

var (
	metricKindGauge metricKind = "gauge"
	metricKindCount metricKind = "count"
)

type metric struct {
	name  string
	kind  metricKind
	value float64
	// Unix timestamp
	ts     float64
	tags   []string
	common bool
}

// TODO: Can there be identically named/tagged metrics with a "common" and "not
// common" variant?

func newmetric(name string, kind metricKind, tags []string, common bool) *metric {
	return &metric{
		name:   name,
		kind:   kind,
		tags:   append([]string{}, tags...),
		common: common,
	}
}

func metricKey(name string, tags []string) string {
	return name + strings.Join(tags, "-")
}

// Gauge sets the value for a gauge with the given name and tags. If the metric
// is not language-specific, common should be set to true
func (c *Client) Gauge(name string, value float64, tags []string, common bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	key := metricKey(name, tags)
	m, ok := c.metrics[key]
	if !ok {
		m = newmetric(name, metricKindGauge, tags, common)
		c.metrics[key] = m
	}
	m.value = value
	m.ts = float64(time.Now().Unix())
	c.newMetrics = true
}

// Count adds the value to a count with the given name and tags. If the metric
// is not language-specific, common should be set to true
func (c *Client) Count(name string, value float64, tags []string, common bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	key := metricKey(name, tags)
	m, ok := c.metrics[key]
	if !ok {
		m = newmetric(name, metricKindCount, tags, common)
		c.metrics[key] = m
	}
	m.value += value
	m.ts = float64(time.Now().Unix())
	c.newMetrics = true
}

// flush sends any outstanding telemetry messages and aggregated metrics to be
// sent to the backend. Requests are sent in the background. Should be called
// with c.mu locked
func (c *Client) flush() {
	submissions := make([]*Request, 0, len(c.requests)+1)
	if c.newMetrics {
		c.newMetrics = false
		r := c.newRequest(RequestTypeGenerateMetrics)
		payload := &Metrics{
			Namespace: c.Namespace,
		}
		for _, m := range c.metrics {
			s := Series{
				Metric: m.name,
				Type:   string(m.kind),
				Tags:   m.tags,
				Common: m.common,
			}
			s.Points = [][2]float64{{m.ts, m.value}}
			payload.Series = append(payload.Series, s)
		}
		r.Body.Payload = payload
		submissions = append(submissions, r)
	}

	// copy over requests so we can do the actual submission without holding
	// the lock. Zero out the old stuff so we don't leak references
	for i, r := range c.requests {
		submissions = append(submissions, r)
		c.requests[i] = nil
	}
	c.requests = c.requests[:0]

	go func() {
		for _, r := range submissions {
			r.submit()
		}
	}()
}

var (
	osName        string
	osNameOnce    sync.Once
	osVersion     string
	osVersionOnce sync.Once
)

// XXX: is it actually safe to cache osName and osVersion? For example, can the
// kernel be updated without stopping execution?

func getOSName() string {
	osNameOnce.Do(func() { osName = osinfo.OSName() })
	return osName
}

func getOSVersion() string {
	osVersionOnce.Do(func() { osVersion = osinfo.OSVersion() })
	return osVersion
}

// newRequests populates a request with the common fields shared by all requests
// sent through this Client
func (c *Client) newRequest(t RequestType) *Request {
	c.seqID++
	body := &Body{
		APIVersion:  "v2",
		RequestType: t,
		TracerTime:  time.Now().Unix(),
		RuntimeID:   globalconfig.RuntimeID(),
		SeqID:       c.seqID,
		Debug:       c.debug,
		Application: Application{
			ServiceName:     c.Service,
			Env:             c.Env,
			ServiceVersion:  c.Version,
			TracerVersion:   version.Tag,
			LanguageName:    "go",
			LanguageVersion: runtime.Version(),
		},
		Host: Host{
			Hostname:  hostname,
			OS:        getOSName(),
			OSVersion: getOSVersion(),
			// TODO (lievan): arch, kernel stuff?
		},
	}
	header := &http.Header{
		"DD-API-KEY":                 {c.APIKey}, // DD-API-KEY is required as of v2
		"Content-Type":               {"application/json"},
		"DD-Telemetry-API-Version":   {"v1"},
		"DD-Telemetry-Request-Type":  {string(t)},
		"DD-Client-Library-Language": {"go"},
		"DD-Client-Library-Version":  {version.Tag},
		"DD-Agent-Env":               {c.Env},
		"DD-Agent-Hostname":          {hostname},
		"Datadog-Container-ID":       {internal.ContainerID()},
	}
	client := c.Client
	if client == nil {
		client = defaultHTTPClient
	}
	return &Request{Body: body,
		Header:          header,
		HTTPClient:      client,
		URL:             c.URL,
		TelemetryClient: c}
}

func (r *Request) submit() error {
	if r.TelemetryClient == nil {
		return fmt.Errorf("all telemetry requests must be associated with a telemetry client")
	}
	retry, err := r._submit()

	if err == nil {
		// submitting to the telemetry client's intended
		// URL succeeded - turn logging back on
		r.TelemetryClient.logging(true)
	} else if retry {
		// retry telemetry submissions in instances where the teletry client has trouble
		// connecting with the agent
		r.TelemetryClient.log("telemetry submission failed, retrying with agentless: %s", err)
		r.URL = getAgentlessURL()
		_, err := r._submit()
		if err != nil {
			r.TelemetryClient.log("retrying with agentless telemetry failed: %s", err)
		}
		// turn off logging after a failed submission to avoid spamming the user with
		// telemetry error messages
		r.TelemetryClient.logging(false)
	}
	return err
}

func (r *Request) _submit() (retry bool, err error) {
	b, err := json.Marshal(r.Body)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequest(http.MethodPost, r.URL, bytes.NewReader(b))
	if err != nil {
		return false, err
	}
	req.Header = *r.Header

	req.ContentLength = int64(len(b))

	client := r.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return true && (r.URL != getAgentlessURL()), err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return true && (r.URL != getAgentlessURL()), errBadStatus(resp.StatusCode)
	}
	return false, nil
}

type errBadStatus int

func (e errBadStatus) Error() string { return fmt.Sprintf("bad HTTP response status %d", e) }

// scheduleSubmit queues a request to be sent to the backend. Should be called
// with c.mu locked
func (c *Client) scheduleSubmit(r *Request) {
	c.requests = append(c.requests, r)
}

func (c *Client) backgroundHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	// TODO (evan.li): thoughts on calling go func here?
	//				   don't want the request to block while holding the lock
	go func() {
		err := c.newRequest(RequestTypeAppHeartbeat).submit()
		if err != nil {
			c.log("heartbeat failed: %s", err)
		}
	}()
	c.heartbeatT.Reset(c.heartbeatInterval)
}

func (c *Client) backgroundFlush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	c.flush()
	c.flushT.Reset(c.SubmissionInterval)
}

// logging is used to turn logging on/off
func (c *Client) logging(logging bool) {
	c.logLock.Lock()
	defer c.logLock.Unlock()
	c.Logging = logging
}

// Default resets the telemetry client to default values
func (c *Client) Default() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyDefaultOps()
	c.readEnvVars()
}

func SetAgentlessEndpoint(endpoint string) string {
	agentlessEndpointLock.Lock()
	defer agentlessEndpointLock.Unlock()
	prev := agentlessURL
	agentlessURL = endpoint
	return prev
}

func init() {
	GlobalClient = new(Client)
	GlobalClient.Default()
}
