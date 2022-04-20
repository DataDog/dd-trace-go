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
	"net/url"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
)

var (
	// copied from dd-trace-go/profiler
	defaultClient = &http.Client{
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
	hostname string
)

// Default values for determining where to upload telemetry
const (
	DefaultAgentAddress            = "http://localhost:8126"
	AgentTelemetryEndpoint         = "/telemetry/proxy/api/v2/apmtelemetry"
	DefaultDirectTelemetryEndpoint = "https://instrumentation-telemetry-intake.datadoghq.com/"
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
	// URL for the Datadog agent or Datadog telemetry endpoint, including
	// the scheme (e.g. "http", "https"). If pointing to a Datadog agent,
	// the correct upload endpoint will be appended, so only the scheme and
	// host need to be provided (e.g. "http://localhost:8126")
	//
	// Defaults to DefaultAgentAddress if no API key is provided, or else
	// DefaultDirectTelemetryEndpoint.
	URL string
	// APIKey should be supplied if the endpoint is not a Datadog agent,
	// i.e. you are sending telemetry directly to Datadog
	APIKey string
	// How often to send batched requests. Defaults to 60s
	SubmissionInterval time.Duration

	// e.g. "tracers", "profilers", "appsec"
	Namespace string

	// App-specific information
	Service string
	Env     string
	Version string

	// Determines whether telemetry should actually run.
	// Defaults to false, but will be overridden by the environment variable
	// DD_INSTRUMENTATION_TELEMETRY_ENABLED is set to 0 or false
	Disabled bool

	// Optional destination to record submission-related logging events
	Logger interface {
		Printf(msg string, args ...interface{})
	}

	// mu guards all of the following fields
	mu sync.Mutex
	// started is true in between when Start() returns and the next call to
	// Stop()
	started bool
	// seqID is a sequence number used to order telemetry messages by
	// the back end.
	seqID int64
	// t is used to schedule flushing outstanding messages
	t *time.Timer
	// requests hold all messages which don't need to be immediately sent
	requests []*Request
	// metrics holds un-sent metrics that will be aggregated the next time
	// metrics are sent
	metrics    map[string]*metric
	newMetrics bool
}

func (c *Client) log(msg string, args ...interface{}) {
	if c.Logger == nil {
		return
	}
	c.Logger.Printf(msg, args...)
}

// Start registers that the app has begun running with the given integrations
// and configuration.
func (c *Client) Start(integrations []Integration, configuration []Configuration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	enabled := os.Getenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED")
	if c.Disabled || enabled == "0" || enabled == "false" {
		return
	}
	if c.started {
		return
	}
	c.started = true

	// XXX: Should we let metrics persist between starting and stopping?
	c.metrics = make(map[string]*metric)

	payload := &AppStarted{
		Integrations:  append([]Integration{}, integrations...),
		Configuration: append([]Configuration{}, configuration...),
	}
	deps, ok := debug.ReadBuildInfo()
	if ok {
		for _, dep := range deps.Deps {
			payload.Dependencies = append(payload.Dependencies,
				Dependency{
					Name:    dep.Path,
					Version: dep.Version,
					// TODO: Neither of the types in the API
					// docs (this or "SharedSystemLibrary")
					// describe Go dependencies well
					Type: "PlatformStandard",
				},
			)
		}
	}

	fromEnvOrDefault := func(key, def string) string {
		if v := os.Getenv(key); len(v) > 0 {
			return v
		}
		return def
	}
	c.Service = fromEnvOrDefault("DD_SERVICE", c.Service)
	if len(c.Service) == 0 {
		if name := globalconfig.ServiceName(); len(name) != 0 {
			c.Service = name
		} else {
			// I think service *has* to be something?
			c.Service = "unnamed-go-service"
		}
	}
	c.Env = fromEnvOrDefault("DD_ENV", c.Env)
	c.Version = fromEnvOrDefault("DD_VERSION", c.Version)

	c.APIKey = fromEnvOrDefault("DD_API_KEY", c.APIKey)
	if len(c.APIKey) == 0 {
		if len(c.URL) == 0 {
			c.URL = DefaultAgentAddress
		}
		u, err := url.Parse(c.URL)
		if err != nil {
			c.log("parsing given URL (%s) failed: %s", c.URL, err)
			return
		}
		u.Path = path.Join(u.Path, AgentTelemetryEndpoint)
		c.URL = u.String()
	} else if len(c.URL) == 0 {
		c.URL = DefaultDirectTelemetryEndpoint
	}

	r := c.newRequest(RequestTypeAppStarted)
	r.Payload = payload
	c.scheduleSubmit(r)
	c.flush()

	if c.SubmissionInterval == 0 {
		c.SubmissionInterval = 60 * time.Second
	}
	c.t = time.AfterFunc(c.SubmissionInterval, c.backgroundFlush)
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
	c.t.Stop()
	// close request types have no body
	r := c.newRequest(RequestTypeAppClosing)
	c.scheduleSubmit(r)
	c.flush()
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
			Namespace:   c.Namespace,
			LibLanguage: "golang",
			LibVersion:  version.Tag,
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
		r.Payload = payload
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
			err := c.submit(r)
			if err != nil {
				c.log("telemetry submission failed: %s", err)
			}
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
	seqID := atomic.AddInt64(&c.seqID, 1)
	return &Request{
		APIVersion:  "v1",
		RequestType: t,
		TracerTime:  time.Now().Unix(),
		RuntimeID:   globalconfig.RuntimeID(),
		SeqID:       seqID,
		Application: Application{
			ServiceName:     c.Service,
			Env:             c.Env,
			ServiceVersion:  c.Version,
			TracerVersion:   version.Tag,
			LanguageName:    "golang",
			LanguageVersion: runtime.Version(),
		},
		Host: Host{
			Hostname:    hostname,
			ContainerID: internal.ContainerID(),
			OS:          getOSName(),
			OSVersion:   getOSVersion(),
		},
	}
}

// submit posts a telemetry request to the backend
func (c *Client) submit(r *Request) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, c.URL, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = http.Header{
		"Content-Type":              {"application/json"},
		"DD-Telemetry-API-Version":  {"v1"},
		"DD-Telemetry-Request-Type": {string(r.RequestType)},
	}
	if len(c.APIKey) > 0 {
		req.Header.Add("DD-API-Key", c.APIKey)
	}
	req.ContentLength = int64(len(b))

	resp, err := defaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return errBadStatus(resp.StatusCode)
	}
	return nil
}

type errBadStatus int

func (e errBadStatus) Error() string { return fmt.Sprintf("bad HTTP response status %d", e) }

// scheduleSubmit queues a request to be sent to the backend. Should be called
// with c.mu locked
func (c *Client) scheduleSubmit(r *Request) {
	c.requests = append(c.requests, r)
}

func (c *Client) backgroundFlush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return
	}
	r := c.newRequest(RequestTypeAppHeartbeat)
	c.scheduleSubmit(r)
	c.flush()
	c.t.Reset(c.SubmissionInterval)
}
