// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package pipelines

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/DataDog/datadog-go/statsd"
)

var (
	// defaultSocketAPM specifies the socket path to use for connecting to the trace-agent.
	// Replaced in tests
	defaultSocketAPM = "/var/run/datadog/apm.socket"

	// defaultSocketDSD specifies the socket path to use for connecting to the statsd server.
	// Replaced in tests
	defaultSocketDSD = "/var/run/datadog/dsd.socket"
)

// config holds the tracer configuration.
type config struct {
	// features holds the capabilities of the agent and determines some
	// of the behaviour of the tracer.
	features agentFeatures
	// logToStdout reports whether we should log all traces to the standard
	// output instead of using the agent. This is used in Lambda environments.
	logToStdout bool
	// logStartup, when true, causes various startup info to be written
	// when the tracer starts.
	logStartup bool
	// service specifies the name of this application.
	service string
	// env contains the environment that this application will run under.
	env string
	// agentAddr specifies the hostname and port of the agent where the traces
	// are sent to.
	agentAddr string
	// globalTags holds a set of tags that will be automatically applied to
	// all spans.
	globalTags map[string]interface{}
	// httpClient specifies the HTTP client to be used by the agent's transport.
	httpClient *http.Client
	// hostname is automatically assigned when the DD_TRACE_REPORT_HOSTNAME is set to true,
	// and is added as a special tag to the root span of traces.
	hostname string
	// dogstatsdAddr specifies the address to connect for sending metrics to the
	// Datadog Agent. If not set, it defaults to "localhost:8125" or to the
	// combination of the environment variables DD_AGENT_HOST and DD_DOGSTATSD_PORT.
	dogstatsdAddr string
	// statsd is used for tracking metrics associated with the runtime and the tracer.
	statsd    statsd.ClientInterface
	site      string
	apiKey    string
	agentLess bool
}

// StartOption represents a function that can be provided as a parameter to Start.
type StartOption func(*config)

// newConfig renders the tracer configuration based on defaults, environment variables
// and passed user opts.
func newConfig(opts ...StartOption) *config {
	c := new(config)
	c.agentAddr = defaultAddress
	c.httpClient = defaultHTTPClient()
	if v := os.Getenv("DD_ENV"); v != "" {
		c.env = v
	}
	if v := os.Getenv("DD_SERVICE"); v != "" {
		c.service = v
	}
	for _, fn := range opts {
		fn(c)
	}
	if c.env == "" {
		if v, ok := c.globalTags["env"]; ok {
			if e, ok := v.(string); ok {
				c.env = e
			}
		}
	}
	if c.service == "" {
		if v, ok := c.globalTags["service"]; ok {
			if s, ok := v.(string); ok {
				c.service = s
			}
		} else {
			c.service = filepath.Base(os.Args[0])
		}
	}
	c.loadAgentFeatures()
	if c.statsd == nil {
		// configure statsd client
		addr := c.dogstatsdAddr
		if addr == "" {
			// no config defined address; use defaults
			addr = defaultDogstatsdAddr()
		}
		if agentport := c.features.StatsdPort; agentport > 0 {
			// the agent reported a non-standard port
			host, _, err := net.SplitHostPort(addr)
			if err == nil {
				// we have a valid host:port address; replace the port because
				// the agent knows better
				if host == "" {
					host = defaultHostname
				}
				addr = net.JoinHostPort(host, strconv.Itoa(agentport))
			}
			// not a valid TCP address, leave it as it is (could be a socket connection)
		}
		c.dogstatsdAddr = addr
		client, err := statsd.New(addr, statsd.WithMaxMessagesPerPayload(40), statsd.WithTags(statsTags(c)))
		if err != nil {
			log.Printf("INFO: Runtime and health metrics disabled: %v", err)
			c.statsd = &statsd.NoOpClient{}
		} else {
			c.statsd = client
		}
	}
	return c
}

// defaultHTTPClient returns the default http.Client to start the tracer with.
func defaultHTTPClient() *http.Client {
	if _, err := os.Stat(defaultSocketAPM); err == nil {
		// we have the UDS socket file, use it
		return udsClient(defaultSocketAPM)
	}
	return defaultClient
}

// udsClient returns a new http.Client which connects using the given UDS socket path.
func udsClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return defaultDialer.DialContext(ctx, "unix", (&net.UnixAddr{
					Name: socketPath,
					Net:  "unix",
				}).String())
			},
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: defaultHTTPTimeout,
	}
}

// defaultDogstatsdAddr returns the default connection address for Dogstatsd.
func defaultDogstatsdAddr() string {
	envHost, envPort := os.Getenv("DD_AGENT_HOST"), os.Getenv("DD_DOGSTATSD_PORT")
	if _, err := os.Stat(defaultSocketDSD); err == nil && envHost == "" && envPort == "" {
		// socket exists and user didn't specify otherwise via env vars
		return "unix://" + defaultSocketDSD
	}
	host, port := defaultHostname, "8125"
	if envHost != "" {
		host = envHost
	}
	if envPort != "" {
		port = envPort
	}
	return net.JoinHostPort(host, port)
}

// agentFeatures holds information about the trace-agent's capabilities.
// When running WithLambdaMode, a zero-value of this struct will be used
// as features.
type agentFeatures struct {
	// PipelineStats reports whether the agent can receive pipeline stats on
	// the /v0.1/pipeline_stats endpoint.
	PipelineStats bool
	// StatsdPort specifies the Dogstatsd port as provided by the agent.
	// If it's the default, it will be 0, which means 8125.
	StatsdPort int
}

// loadAgentFeatures queries the trace-agent for its capabilities and updates
// the tracer's behaviour.
func (c *config) loadAgentFeatures() {
	c.features = agentFeatures{}
	if c.logToStdout {
		// there is no agent; all features off
		return
	}
	resp, err := c.httpClient.Get(fmt.Sprintf("http://%s/info", c.agentAddr))
	if err != nil {
		log.Printf("ERROR: Loading features: %v", err)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		// agent is older than 7.28.0, features not discoverable
		return
	}
	defer resp.Body.Close()
	type infoResponse struct {
		Endpoints  []string `json:"endpoints"`
		StatsdPort int      `json:"statsd_port"`
	}
	var info infoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Printf("ERROR: Decoding features: %v", err)
		return
	}
	c.features.StatsdPort = info.StatsdPort
	for _, endpoint := range info.Endpoints {
		switch endpoint {
		case "/v0.1/pipeline_stats":
			c.features.PipelineStats = true
			log.Printf("INFO: Enable pipeline stats.")
		}
	}
}

func statsTags(c *config) []string {
	tags := []string{
		"lang:go",
		"lang_version:" + runtime.Version(),
	}
	if c.service != "" {
		tags = append(tags, "service:"+c.service)
	}
	if c.env != "" {
		tags = append(tags, "env:"+c.env)
	}
	if c.hostname != "" {
		tags = append(tags, "host:"+c.hostname)
	}
	for k, v := range c.globalTags {
		if vstr, ok := v.(string); ok {
			tags = append(tags, k+":"+vstr)
		}
	}
	return tags
}

// withNoopStats is used for testing to disable statsd client
func withNoopStats() StartOption {
	return func(c *config) {
		c.statsd = &statsd.NoOpClient{}
	}
}

// WithService sets the default service name for the program.
func WithService(name string) StartOption {
	return func(c *config) {
		c.service = name
	}
}

// WithAgentAddr sets the address where the agent is located. The default is
// localhost:8126. It should contain both host and port.
func WithAgentAddr(addr string) StartOption {
	return func(c *config) {
		c.agentAddr = addr
	}
}

// WithEnv sets the environment to which all traces started by the tracer will be submitted.
// The default value is the environment variable DD_ENV, if it is set.
func WithEnv(env string) StartOption {
	return func(c *config) {
		c.env = env
	}
}

// WithDogstatsdAddress specifies the address to connect to for sending metrics
// to the Datadog Agent. If not set, it defaults to "localhost:8125" or to the
// combination of the environment variables DD_AGENT_HOST and DD_DOGSTATSD_PORT.
// This option is in effect when WithRuntimeMetrics is enabled.
func WithDogstatsdAddress(addr string) StartOption {
	return func(cfg *config) {
		cfg.dogstatsdAddr = addr
	}
}

// WithSite starts the pipeline processor with a given site to send data to.
func WithSite(site string) StartOption {
	return func(c *config) {
		c.site = site
	}
}

// WithAgentLess starts the pipeline processor in a mode where stats are sent directly to the datadog backend
// instead of going through the agent.
func WithAgentLess(apiKey string) StartOption {
	return func(c *config) {
		c.apiKey = apiKey
		c.agentLess = true
	}
}
