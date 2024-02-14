// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	v2traceinternal "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/DataDog/datadog-go/v5/statsd"
)

var contribIntegrations = map[string]struct {
	name     string // user readable name for startup logs
	imported bool   // true if the user has imported the integration
}{
	"github.com/99designs/gqlgen":                   {"gqlgen", false},
	"github.com/aws/aws-sdk-go":                     {"AWS SDK", false},
	"github.com/aws/aws-sdk-go-v2":                  {"AWS SDK v2", false},
	"github.com/bradfitz/gomemcache":                {"Memcache", false},
	"cloud.google.com/go/pubsub.v1":                 {"Pub/Sub", false},
	"github.com/confluentinc/confluent-kafka-go":    {"Kafka (confluent)", false},
	"github.com/confluentinc/confluent-kafka-go/v2": {"Kafka (confluent) v2", false},
	"database/sql":                                  {"SQL", false},
	"github.com/dimfeld/httptreemux/v5":             {"HTTP Treemux", false},
	"github.com/elastic/go-elasticsearch/v6":        {"Elasticsearch v6", false},
	"github.com/emicklei/go-restful":                {"go-restful", false},
	"github.com/emicklei/go-restful/v3":             {"go-restful v3", false},
	"github.com/garyburd/redigo":                    {"Redigo (dep)", false},
	"github.com/gin-gonic/gin":                      {"Gin", false},
	"github.com/globalsign/mgo":                     {"MongoDB (mgo)", false},
	"github.com/go-chi/chi":                         {"chi", false},
	"github.com/go-chi/chi/v5":                      {"chi v5", false},
	"github.com/go-pg/pg/v10":                       {"go-pg v10", false},
	"github.com/go-redis/redis":                     {"Redis", false},
	"github.com/go-redis/redis/v7":                  {"Redis v7", false},
	"github.com/go-redis/redis/v8":                  {"Redis v8", false},
	"go.mongodb.org/mongo-driver":                   {"MongoDB", false},
	"github.com/gocql/gocql":                        {"Cassandra", false},
	"github.com/gofiber/fiber/v2":                   {"Fiber", false},
	"github.com/gomodule/redigo":                    {"Redigo", false},
	"google.golang.org/api":                         {"Google API", false},
	"google.golang.org/grpc":                        {"gRPC", false},
	"google.golang.org/grpc/v12":                    {"gRPC v12", false},
	"gopkg.in/jinzhu/gorm.v1":                       {"Gorm (gopkg)", false},
	"github.com/gorilla/mux":                        {"Gorilla Mux", false},
	"gorm.io/gorm.v1":                               {"Gorm v1", false},
	"github.com/graph-gophers/graphql-go":           {"GraphQL", false},
	"github.com/hashicorp/consul/api":               {"Consul", false},
	"github.com/hashicorp/vault/api":                {"Vault", false},
	"github.com/jinzhu/gorm":                        {"Gorm", false},
	"github.com/jmoiron/sqlx":                       {"SQLx", false},
	"github.com/julienschmidt/httprouter":           {"HTTP Router", false},
	"k8s.io/client-go/kubernetes":                   {"Kubernetes", false},
	"github.com/labstack/echo":                      {"echo", false},
	"github.com/labstack/echo/v4":                   {"echo v4", false},
	"github.com/miekg/dns":                          {"miekg/dns", false},
	"net/http":                                      {"HTTP", false},
	"gopkg.in/olivere/elastic.v5":                   {"Elasticsearch v5", false},
	"gopkg.in/olivere/elastic.v3":                   {"Elasticsearch v3", false},
	"github.com/redis/go-redis/v9":                  {"Redis v9", false},
	"github.com/segmentio/kafka-go":                 {"Kafka v0", false},
	"github.com/IBM/sarama":                         {"IBM sarama", false},
	"github.com/Shopify/sarama":                     {"Shopify sarama", false},
	"github.com/sirupsen/logrus":                    {"Logrus", false},
	"github.com/syndtr/goleveldb":                   {"LevelDB", false},
	"github.com/tidwall/buntdb":                     {"BuntDB", false},
	"github.com/twitchtv/twirp":                     {"Twirp", false},
	"github.com/urfave/negroni":                     {"Negroni", false},
	"github.com/valyala/fasthttp":                   {"FastHTTP", false},
	"github.com/zenazn/goji":                        {"Goji", false},
}

var (
	// defaultSocketAPM specifies the socket path to use for connecting to the trace-agent.
	// Replaced in tests
	defaultSocketAPM = "/var/run/datadog/apm.socket"

	// defaultSocketDSD specifies the socket path to use for connecting to the statsd server.
	// Replaced in tests
	defaultSocketDSD = "/var/run/datadog/dsd.socket"

	// defaultMaxTagsHeaderLen specifies the default maximum length of the X-Datadog-Tags header value.
	defaultMaxTagsHeaderLen = 128
)

// config holds the tracer configuration.
type config struct {
	// debug, when true, writes details to logs.
	debug bool

	// agent holds the capabilities of the agent and determines some
	// of the behaviour of the tracer.
	agent agentFeatures

	// integrations reports if the user has instrumented a Datadog integration and
	// if they have a version of the library available to integrate.
	integrations map[string]integrationConfig

	// featureFlags specifies any enabled feature flags.
	featureFlags map[string]struct{}

	// logToStdout reports whether we should log all traces to the standard
	// output instead of using the agent. This is used in Lambda environments.
	logToStdout bool

	// sendRetries is the number of times a trace payload send is retried upon
	// failure.
	sendRetries int

	// logStartup, when true, causes various startup info to be written
	// when the tracer starts.
	logStartup bool

	// serviceName specifies the name of this application.
	serviceName string

	// universalVersion, reports whether span service name and config service name
	// should match to set application version tag. False by default
	universalVersion bool

	// version specifies the version of this application
	version string

	// env contains the environment that this application will run under.
	env string

	// sampler specifies the sampler that will be used for sampling traces.
	sampler Sampler

	// agentURL is the agent URL that receives traces from the tracer.
	agentURL *url.URL

	// serviceMappings holds a set of service mappings to dynamically rename services
	serviceMappings map[string]string

	// globalTags holds a set of tags that will be automatically applied to
	// all spans.
	globalTags dynamicConfig[map[string]interface{}]

	// transport specifies the Transport interface which will be used to send data to the agent.
	transport transport

	// propagator propagates span context cross-process
	propagator Propagator

	// httpClient specifies the HTTP client to be used by the agent's transport.
	httpClient *http.Client

	// hostname is automatically assigned when the DD_TRACE_REPORT_HOSTNAME is set to true,
	// and is added as a special tag to the root span of traces.
	hostname string

	// logger specifies the logger to use when printing errors. If not specified, the "log" package
	// will be used.
	logger ddtrace.Logger

	// runtimeMetrics specifies whether collection of runtime metrics is enabled.
	runtimeMetrics bool

	// dogstatsdAddr specifies the address to connect for sending metrics to the
	// Datadog Agent. If not set, it defaults to "localhost:8125" or to the
	// combination of the environment variables DD_AGENT_HOST and DD_DOGSTATSD_PORT.
	dogstatsdAddr string

	// statsdClient is set when a user provides a custom statsd client for tracking metrics
	// associated with the runtime and the tracer.
	statsdClient internal.StatsdClient

	// spanRules contains user-defined rules to determine the sampling rate to apply
	// to a single span without affecting the entire trace
	spanRules []SamplingRule

	// traceRules contains user-defined rules to determine the sampling rate to apply
	// to the entire trace if any spans satisfy the criteria
	traceRules []SamplingRule

	// tickChan specifies a channel which will receive the time every time the tracer must flush.
	// It defaults to time.Ticker; replaced in tests.
	tickChan <-chan time.Time

	// noDebugStack disables the collection of debug stack traces globally. No traces reporting
	// errors will record a stack trace when this option is set.
	noDebugStack bool

	// profilerHotspots specifies whether profiler Code Hotspots is enabled.
	profilerHotspots bool

	// profilerEndpoints specifies whether profiler endpoint filtering is enabled.
	profilerEndpoints bool

	// enabled reports whether tracing is enabled.
	enabled bool

	// enableHostnameDetection specifies whether the tracer should enable hostname detection.
	enableHostnameDetection bool

	// spanAttributeSchemaVersion holds the selected DD_TRACE_SPAN_ATTRIBUTE_SCHEMA version.
	spanAttributeSchemaVersion int

	// peerServiceDefaultsEnabled indicates whether the peer.service tag calculation is enabled or not.
	peerServiceDefaultsEnabled bool

	// peerServiceMappings holds a set of service mappings to dynamically rename peer.service values.
	peerServiceMappings map[string]string

	// debugAbandonedSpans controls if the tracer should log when old, open spans are found
	debugAbandonedSpans bool

	// spanTimeout represents how old a span can be before it should be logged as a possible
	// misconfiguration
	spanTimeout time.Duration

	// partialFlushMinSpans is the number of finished spans in a single trace to trigger a
	// partial flush, or 0 if partial flushing is disabled.
	// Value from DD_TRACE_PARTIAL_FLUSH_MIN_SPANS, default 1000.
	partialFlushMinSpans int

	// partialFlushEnabled specifices whether the tracer should enable partial flushing. Value
	// from DD_TRACE_PARTIAL_FLUSH_ENABLED, default false.
	partialFlushEnabled bool

	// statsComputationEnabled enables client-side stats computation (aka trace metrics).
	statsComputationEnabled bool

	// dataStreamsMonitoringEnabled specifies whether the tracer should enable monitoring of data streams
	dataStreamsMonitoringEnabled bool

	// orchestrionCfg holds Orchestrion (aka auto-instrumentation) configuration.
	// Only used for telemetry currently.
	orchestrionCfg orchestrionConfig

	// traceSampleRate holds the trace sample rate.
	traceSampleRate dynamicConfig[float64]

	// headerAsTags holds the header as tags configuration.
	headerAsTags dynamicConfig[[]string]
}

// orchestrionConfig contains Orchestrion configuration.
type orchestrionConfig struct {
	// Enabled indicates whether this tracer was instanciated via Orchestrion.
	Enabled bool `json:"enabled"`

	// Metadata holds Orchestrion specific metadata (e.g orchestrion version, mode (toolexec or manual) etc..)
	Metadata map[string]string `json:"metadata,omitempty"`
}

// HasFeature reports whether feature f is enabled.
func (c *config) HasFeature(f string) bool {
	_, ok := c.featureFlags[strings.TrimSpace(f)]
	return ok
}

// StartOption represents a function that can be provided as a parameter to Start.
type StartOption = v2.StartOption

// maxPropagatedTagsLength limits the size of DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH to prevent HTTP 413 responses.
const maxPropagatedTagsLength = 512

// partialFlushMinSpansDefault is the default number of spans for partial flushing, if enabled.
const partialFlushMinSpansDefault = 1000

func newStatsdClient(c *config) (internal.StatsdClient, error) {
	if c.statsdClient != nil {
		return c.statsdClient, nil
	}

	client, err := statsd.New(c.dogstatsdAddr, statsd.WithMaxMessagesPerPayload(40), statsd.WithTags(statsTags(c)))
	if err != nil {
		return &statsd.NoOpClient{}, err
	}
	return client, nil
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

type integrationConfig struct {
	Instrumented bool   `json:"instrumented"`      // indicates if the user has imported and used the integration
	Available    bool   `json:"available"`         // indicates if the user is using a library that can be used with DataDog integrations
	Version      string `json:"available_version"` // if available, indicates the version of the library the user has
}

// agentFeatures holds information about the trace-agent's capabilities.
// When running WithLambdaMode, a zero-value of this struct will be used
// as features.
type agentFeatures struct {
	// DropP0s reports whether it's ok for the tracer to not send any
	// P0 traces to the agent.
	DropP0s bool

	// Stats reports whether the agent can receive client-computed stats on
	// the /v0.6/stats endpoint.
	Stats bool

	// DataStreams reports whether the agent can receive data streams stats on
	// the /v0.1/pipeline_stats endpoint.
	DataStreams bool

	// StatsdPort specifies the Dogstatsd port as provided by the agent.
	// If it's the default, it will be 0, which means 8125.
	StatsdPort int

	// featureFlags specifies all the feature flags reported by the trace-agent.
	featureFlags map[string]struct{}
}

// HasFlag reports whether the agent has set the feat feature flag.
func (a *agentFeatures) HasFlag(feat string) bool {
	_, ok := a.featureFlags[feat]
	return ok
}

// loadAgentFeatures queries the trace-agent for its capabilities and updates
// the tracer's behaviour.
func loadAgentFeatures(logToStdout bool, agentURL *url.URL, httpClient *http.Client) (features agentFeatures) {
	if logToStdout {
		// there is no agent; all features off
		return
	}
	resp, err := httpClient.Get(fmt.Sprintf("%s/info", agentURL))
	if err != nil {
		log.Error("Loading features: %v", err)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		// agent is older than 7.28.0, features not discoverable
		return
	}
	defer resp.Body.Close()
	type infoResponse struct {
		Endpoints     []string `json:"endpoints"`
		ClientDropP0s bool     `json:"client_drop_p0s"`
		StatsdPort    int      `json:"statsd_port"`
		FeatureFlags  []string `json:"feature_flags"`
	}
	var info infoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		log.Error("Decoding features: %v", err)
		return
	}
	features.DropP0s = info.ClientDropP0s
	features.StatsdPort = info.StatsdPort
	for _, endpoint := range info.Endpoints {
		switch endpoint {
		case "/v0.6/stats":
			features.Stats = true
		case "/v0.1/pipeline_stats":
			features.DataStreams = true
		}
	}
	features.featureFlags = make(map[string]struct{}, len(info.FeatureFlags))
	for _, flag := range info.FeatureFlags {
		features.featureFlags[flag] = struct{}{}
	}
	return features
}

// MarkIntegrationImported labels the given integration as imported
func MarkIntegrationImported(integration string) bool {
	s, ok := contribIntegrations[integration]
	if !ok {
		return false
	}
	s.imported = true
	contribIntegrations[integration] = s
	return true
}

func (c *config) loadContribIntegrations(deps []*debug.Module) {
	integrations := map[string]integrationConfig{}
	for _, s := range contribIntegrations {
		integrations[s.name] = integrationConfig{
			Instrumented: s.imported,
		}
	}
	for _, d := range deps {
		p := d.Path
		// special use case, since gRPC does not update version number
		if p == "google.golang.org/grpc" {
			re := regexp.MustCompile(`v(\d.\d)\d*`)
			match := re.FindStringSubmatch(d.Version)
			if match == nil {
				log.Warn("Unable to parse version of GRPC %v", d.Version)
				continue
			}
			ver, err := strconv.ParseFloat(match[1], 32)
			if err != nil {
				log.Warn("Unable to parse version of GRPC %v as a float", d.Version)
				continue
			}
			if ver <= 1.2 {
				p = p + "/v12"
			}
		}
		s, ok := contribIntegrations[p]
		if !ok {
			continue
		}
		conf := integrations[s.name]
		conf.Available = true
		conf.Version = d.Version
		integrations[s.name] = conf
	}
	c.integrations = integrations
}

func (c *config) canComputeStats() bool {
	return c.agent.Stats && (c.HasFeature("discovery") || c.statsComputationEnabled)
}

func (c *config) canDropP0s() bool {
	return c.canComputeStats() && c.agent.DropP0s
}

func statsTags(c *config) []string {
	tags := []string{
		"lang:go",
		"version:" + version.Tag,
		"lang_version:" + runtime.Version(),
	}
	if c.serviceName != "" {
		tags = append(tags, "service:"+c.serviceName)
	}
	if c.env != "" {
		tags = append(tags, "env:"+c.env)
	}
	if c.hostname != "" {
		tags = append(tags, "host:"+c.hostname)
	}
	for k, v := range c.globalTags.get() {
		if vstr, ok := v.(string); ok {
			tags = append(tags, k+":"+vstr)
		}
	}
	return tags
}

// WithFeatureFlags specifies a set of feature flags to enable. Please take into account
// that most, if not all features flags are considered to be experimental and result in
// unexpected bugs.
func WithFeatureFlags(feats ...string) StartOption {
	return v2.WithFeatureFlags(feats...)
}

// WithLogger sets logger as the tracer's error printer.
// Diagnostic and startup tracer logs are prefixed to simplify the search within logs.
// If JSON logging format is required, it's possible to wrap tracer logs using an existing JSON logger with this
// function. To learn more about this possibility, please visit: https://github.com/DataDog/dd-trace-go/issues/2152#issuecomment-1790586933
func WithLogger(logger ddtrace.Logger) StartOption {
	return v2.WithLogger(logger)
}

// WithPrioritySampling is deprecated, and priority sampling is enabled by default.
// When using distributed tracing, the priority sampling value is propagated in order to
// get all the parts of a distributed trace sampled.
// To learn more about priority sampling, please visit:
// https://docs.datadoghq.com/tracing/getting_further/trace_sampling_and_storage/#priority-sampling-for-distributed-tracing
func WithPrioritySampling() StartOption {
	return nil
}

// WithDebugStack can be used to globally enable or disable the collection of stack traces when
// spans finish with errors. It is enabled by default. This is a global version of the NoDebugStack
// FinishOption.
func WithDebugStack(enabled bool) StartOption {
	return v2.WithDebugStack(enabled)
}

// WithDebugMode enables debug mode on the tracer, resulting in more verbose logging.
func WithDebugMode(enabled bool) StartOption {
	return v2.WithDebugMode(enabled)
}

// WithLambdaMode enables lambda mode on the tracer, for use with AWS Lambda.
// This option is only required if the the Datadog Lambda Extension is not
// running.
func WithLambdaMode(enabled bool) StartOption {
	return v2.WithLambdaMode(enabled)
}

// WithSendRetries enables re-sending payloads that are not successfully
// submitted to the agent.  This will cause the tracer to retry the send at
// most `retries` times.
func WithSendRetries(retries int) StartOption {
	return v2.WithSendRetries(retries)
}

// WithPropagator sets an alternative propagator to be used by the tracer.
func WithPropagator(p Propagator) StartOption {
	return v2.WithPropagator(&propagatorV1Adapter{propagator: p})
}

// WithServiceName is deprecated. Please use WithService.
// If you are using an older version and you are upgrading from WithServiceName
// to WithService, please note that WithService will determine the service name of
// server and framework integrations.
func WithServiceName(name string) StartOption {
	return v2.WithService(name)
}

// WithService sets the default service name for the program.
func WithService(name string) StartOption {
	return v2.WithService(name)
}

// WithGlobalServiceName causes contrib libraries to use the global service name and not any locally defined service name.
// This is synonymous with `DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED`.
func WithGlobalServiceName(enabled bool) StartOption {
	return v2.WithGlobalServiceName(enabled)
}

// WithAgentAddr sets the address where the agent is located. The default is
// localhost:8126. It should contain both host and port.
func WithAgentAddr(addr string) StartOption {
	return v2.WithAgentAddr(addr)
}

// WithEnv sets the environment to which all traces started by the tracer will be submitted.
// The default value is the environment variable DD_ENV, if it is set.
func WithEnv(env string) StartOption {
	return v2.WithEnv(env)
}

// WithServiceMapping determines service "from" to be renamed to service "to".
// This option is is case sensitive and can be used multiple times.
func WithServiceMapping(from, to string) StartOption {
	return v2.WithServiceMapping(from, to)
}

// WithPeerServiceDefaults sets default calculation for peer.service.
func WithPeerServiceDefaults(enabled bool) StartOption {
	return v2.WithPeerServiceDefaults(enabled)
}

// WithPeerServiceMapping determines the value of the peer.service tag "from" to be renamed to service "to".
func WithPeerServiceMapping(from, to string) StartOption {
	return v2.WithPeerServiceMapping(from, to)
}

// WithGlobalTag sets a key/value pair which will be set as a tag on all spans
// created by tracer. This option may be used multiple times.
func WithGlobalTag(k string, v interface{}) StartOption {
	return v2.WithGlobalTag(k, v)
}

// initGlobalTags initializes the globalTags config with the provided init value
func (c *config) initGlobalTags(init map[string]interface{}) {
	apply := func(map[string]interface{}) bool {
		// always set the runtime ID on updates
		c.globalTags.current[ext.RuntimeID] = globalconfig.RuntimeID()
		return true
	}
	c.globalTags = newDynamicConfig[map[string]interface{}]("trace_tags", init, apply, equalMap[string])
}

type samplerV1Adapter struct {
	sampler Sampler
}

// Sample implements tracer.Sampler.
func (sa *samplerV1Adapter) Sample(span *v2.Span) bool {
	s := &v2traceinternal.SpanV2Adapter{Span: span}
	return sa.sampler.Sample(s)
}

// WithSampler sets the given sampler to be used with the tracer. By default
// an all-permissive sampler is used.
func WithSampler(s Sampler) StartOption {
	return v2.WithSampler(&samplerV1Adapter{sampler: s})
}

// WithHTTPRoundTripper is deprecated. Please consider using WithHTTPClient instead.
// The function allows customizing the underlying HTTP transport for emitting spans.
func WithHTTPRoundTripper(r http.RoundTripper) StartOption {
	return WithHTTPClient(&http.Client{
		Transport: r,
		Timeout:   defaultHTTPTimeout,
	})
}

// WithHTTPClient specifies the HTTP client to use when emitting spans to the agent.
func WithHTTPClient(client *http.Client) StartOption {
	return v2.WithHTTPClient(client)
}

// WithUDS configures the HTTP client to dial the Datadog Agent via the specified Unix Domain Socket path.
func WithUDS(socketPath string) StartOption {
	return v2.WithUDS(socketPath)
}

// WithAnalytics allows specifying whether Trace Search & Analytics should be enabled
// for integrations.
func WithAnalytics(on bool) StartOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the global sampling rate for sampling APM events.
func WithAnalyticsRate(rate float64) StartOption {
	return v2.WithAnalyticsRate(rate)
}

// WithRuntimeMetrics enables automatic collection of runtime metrics every 10 seconds.
func WithRuntimeMetrics() StartOption {
	return v2.WithRuntimeMetrics()
}

// WithDogstatsdAddress specifies the address to connect to for sending metrics to the Datadog
// Agent. It should be a "host:port" string, or the path to a unix domain socket.If not set, it
// attempts to determine the address of the statsd service according to the following rules:
//  1. Look for /var/run/datadog/dsd.socket and use it if present. IF NOT, continue to #2.
//  2. The host is determined by DD_AGENT_HOST, and defaults to "localhost"
//  3. The port is retrieved from the agent. If not present, it is determined by DD_DOGSTATSD_PORT, and defaults to 8125
//
// This option is in effect when WithRuntimeMetrics is enabled.
func WithDogstatsdAddress(addr string) StartOption {
	return v2.WithDogstatsdAddress(addr)
}

// WithSamplingRules specifies the sampling rates to apply to spans based on the
// provided rules.
func WithSamplingRules(rules []SamplingRule) StartOption {
	rr := make([]v2.SamplingRule, len(rules))
	for i, r := range rules {
		var ssr []v2.SamplingRule
		if r.ruleType == SamplingRuleSpan {
			ssr = v2.SpanSamplingRules(v2.Rule{
				Rate: r.Rate,
			})
		} else {
			ssr = v2.TraceSamplingRules(v2.Rule{
				Rate: r.Rate,
			})
		}
		rr[i] = ssr[0]
		rr[i].MaxPerSecond = r.MaxPerSecond
		rr[i].Name = r.Name
		rr[i].Resource = r.Resource
		rr[i].Service = r.Service
		rr[i].Tags = r.Tags
	}
	return v2.WithSamplingRules(rr)
}

// WithServiceVersion specifies the version of the service that is running. This will
// be included in spans from this service in the "version" tag, provided that
// span service name and config service name match. Do NOT use with WithUniversalVersion.
func WithServiceVersion(version string) StartOption {
	return v2.WithServiceVersion(version)
}

// WithUniversalVersion specifies the version of the service that is running, and will be applied to all spans,
// regardless of whether span service name and config service name match.
// See: WithService, WithServiceVersion. Do NOT use with WithServiceVersion.
func WithUniversalVersion(version string) StartOption {
	return v2.WithUniversalVersion(version)
}

// WithHostname allows specifying the hostname with which to mark outgoing traces.
func WithHostname(name string) StartOption {
	return v2.WithHostname(name)
}

// WithTraceEnabled allows specifying whether tracing will be enabled
func WithTraceEnabled(enabled bool) StartOption {
	return v2.WithTraceEnabled(enabled)
}

// WithLogStartup allows enabling or disabling the startup log.
func WithLogStartup(enabled bool) StartOption {
	return v2.WithLogStartup(enabled)
}

// WithProfilerCodeHotspots enables the code hotspots integration between the
// tracer and profiler. This is done by automatically attaching pprof labels
// called "span id" and "local root span id" when new spans are created. You
// should not use these label names in your own code when this is enabled. The
// enabled value defaults to the value of the
// DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED env variable or true.
func WithProfilerCodeHotspots(enabled bool) StartOption {
	return v2.WithProfilerCodeHotspots(enabled)
}

// WithProfilerEndpoints enables the endpoints integration between the tracer
// and profiler. This is done by automatically attaching a pprof label called
// "trace endpoint" holding the resource name of the top-level service span if
// its type is "http", "rpc" or "" (default). You should not use this label
// name in your own code when this is enabled. The enabled value defaults to
// the value of the DD_PROFILING_ENDPOINT_COLLECTION_ENABLED env variable or
// true.
func WithProfilerEndpoints(enabled bool) StartOption {
	return v2.WithProfilerEndpoints(enabled)
}

// WithDebugSpansMode enables debugging old spans that may have been
// abandoned, which may prevent traces from being set to the Datadog
// Agent, especially if partial flushing is off.
// This setting can also be configured by setting DD_TRACE_DEBUG_ABANDONED_SPANS
// to true. The timeout will default to 10 minutes, unless overwritten
// by DD_TRACE_ABANDONED_SPAN_TIMEOUT.
// This feature is disabled by default. Turning on this debug mode may
// be expensive, so it should only be enabled for debugging purposes.
func WithDebugSpansMode(timeout time.Duration) StartOption {
	return v2.WithDebugSpansMode(timeout)
}

// WithPartialFlushing enables flushing of partially finished traces.
// This is done after "numSpans" have finished in a single local trace at
// which point all finished spans in that trace will be flushed, freeing up
// any memory they were consuming. This can also be configured by setting
// DD_TRACE_PARTIAL_FLUSH_ENABLED to true, which will default to 1000 spans
// unless overriden with DD_TRACE_PARTIAL_FLUSH_MIN_SPANS. Partial flushing
// is disabled by default.
func WithPartialFlushing(numSpans int) StartOption {
	return v2.WithPartialFlushing(numSpans)
}

// WithStatsComputation enables client-side stats computation, allowing
// the tracer to compute stats from traces. This can reduce network traffic
// to the Datadog Agent, and produce more accurate stats data.
// This can also be configured by setting DD_TRACE_STATS_COMPUTATION_ENABLED to true.
// Client-side stats is off by default.
func WithStatsComputation(enabled bool) StartOption {
	return v2.WithStatsComputation(enabled)
}

// WithOrchestrion configures Orchestrion's auto-instrumentation metadata.
// This option is only intended to be used by Orchestrion https://github.com/DataDog/orchestrion
func WithOrchestrion(metadata map[string]string) StartOption {
	return v2.WithOrchestrion(metadata)
}

// StartSpanOption is a configuration option for StartSpan. It is aliased in order
// to help godoc group all the functions returning it together. It is considered
// more correct to refer to it as the type as the origin, ddtrace.StartSpanOption.
type StartSpanOption = ddtrace.StartSpanOption

// Tag sets the given key/value pair as a tag on the started Span.
func Tag(k string, v interface{}) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		if cfg.Tags == nil {
			cfg.Tags = map[string]interface{}{}
		}
		cfg.Tags[k] = v
	}
}

// ServiceName sets the given service name on the started span. For example "http.server".
func ServiceName(name string) StartSpanOption {
	return Tag(ext.ServiceName, name)
}

// ResourceName sets the given resource name on the started span. A resource could
// be an SQL query, a URL, an RPC method or something else.
func ResourceName(name string) StartSpanOption {
	return Tag(ext.ResourceName, name)
}

// SpanType sets the given span type on the started span. Some examples in the case of
// the Datadog APM product could be "web", "db" or "cache".
func SpanType(name string) StartSpanOption {
	return Tag(ext.SpanType, name)
}

// WithSpanLinks sets span links on the started span.
func WithSpanLinks(links []ddtrace.SpanLink) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.SpanLinks = append(cfg.SpanLinks, links...)
	}
}

var measuredTag = Tag(keyMeasured, 1)

// Measured marks this span to be measured for metrics and stats calculations.
func Measured() StartSpanOption {
	// cache a global instance of this tag: saves one alloc/call
	return measuredTag
}

// WithSpanID sets the SpanID on the started span, instead of using a random number.
// If there is no parent Span (eg from ChildOf), then the TraceID will also be set to the
// value given here.
func WithSpanID(id uint64) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.SpanID = id
	}
}

// ChildOf tells StartSpan to use the given span context as a parent for the
// created span.
func ChildOf(ctx ddtrace.SpanContext) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.Parent = ctx
	}
}

// withContext associates the ctx with the span.
func withContext(ctx context.Context) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.Context = ctx
	}
}

// StartTime sets a custom time as the start time for the created span. By
// default a span is started using the creation time.
func StartTime(t time.Time) StartSpanOption {
	return func(cfg *ddtrace.StartSpanConfig) {
		cfg.StartTime = t
	}
}

// AnalyticsRate sets a custom analytics rate for a span. It decides the percentage
// of events that will be picked up by the App Analytics product. It's represents a
// float64 between 0 and 1 where 0.5 would represent 50% of events.
func AnalyticsRate(rate float64) StartSpanOption {
	if math.IsNaN(rate) {
		return func(cfg *ddtrace.StartSpanConfig) {}
	}
	return Tag(ext.EventSampleRate, rate)
}

// FinishOption is a configuration option for FinishSpan. It is aliased in order
// to help godoc group all the functions returning it together. It is considered
// more correct to refer to it as the type as the origin, ddtrace.FinishOption.
type FinishOption = ddtrace.FinishOption

// FinishTime sets the given time as the finishing time for the span. By default,
// the current time is used.
func FinishTime(t time.Time) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.FinishTime = t
	}
}

// WithError marks the span as having had an error. It uses the information from
// err to set tags such as the error message, error type and stack trace. It has
// no effect if the error is nil.
func WithError(err error) FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.Error = err
	}
}

// NoDebugStack prevents any error presented using the WithError finishing option
// from generating a stack trace. This is useful in situations where errors are frequent
// and performance is critical.
func NoDebugStack() FinishOption {
	return func(cfg *ddtrace.FinishConfig) {
		cfg.NoDebugStack = true
	}
}

// StackFrames limits the number of stack frames included into erroneous spans to n, starting from skip.
func StackFrames(n, skip uint) FinishOption {
	if n == 0 {
		return NoDebugStack()
	}
	return func(cfg *ddtrace.FinishConfig) {
		cfg.StackFrames = n
		cfg.SkipStackFrames = skip
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headerAsTags []string) StartOption {
	return v2.WithHeaderTags(headerAsTags)
}

// UserMonitoringConfig is used to configure what is used to identify a user.
// This configuration can be set by combining one or several UserMonitoringOption with a call to SetUser().
type UserMonitoringConfig = v2.UserMonitoringConfig

// UserMonitoringOption represents a function that can be provided as a parameter to SetUser.
type UserMonitoringOption = v2.UserMonitoringOption

// WithUserMetadata returns the option setting additional metadata of the authenticated user.
// This can be used multiple times and the given data will be tracked as `usr.{key}=value`.
func WithUserMetadata(key, value string) UserMonitoringOption {
	return v2.WithUserMetadata(key, value)
}

// WithUserEmail returns the option setting the email of the authenticated user.
func WithUserEmail(email string) UserMonitoringOption {
	return v2.WithUserEmail(email)
}

// WithUserName returns the option setting the name of the authenticated user.
func WithUserName(name string) UserMonitoringOption {
	return v2.WithUserName(name)
}

// WithUserSessionID returns the option setting the session ID of the authenticated user.
func WithUserSessionID(sessionID string) UserMonitoringOption {
	return v2.WithUserSessionID(sessionID)
}

// WithUserRole returns the option setting the role of the authenticated user.
func WithUserRole(role string) UserMonitoringOption {
	return v2.WithUserRole(role)
}

// WithUserScope returns the option setting the scope (authorizations) of the authenticated user.
func WithUserScope(scope string) UserMonitoringOption {
	return v2.WithUserScope(scope)
}

// WithPropagation returns the option allowing the user id to be propagated through distributed traces.
// The user id is base64 encoded and added to the datadog propagated tags header.
// This option should only be used if you are certain that the user id passed to `SetUser()` does not contain any
// personal identifiable information or any kind of sensitive data, as it will be leaked to other services.
func WithPropagation() UserMonitoringOption {
	return v2.WithPropagation()
}
