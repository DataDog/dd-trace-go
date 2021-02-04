// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/DataDog/datadog-go/statsd"
)

const (
	// DefaultMutexFraction specifies the mutex profile fraction to be used with the mutex profiler.
	// For more information or for changing this value, check MutexProfileFraction
	DefaultMutexFraction = 10

	// DefaultBlockRate specifies the default block profiling rate used by the block profiler.
	// For more information or for changing this value, check BlockProfileRate.
	DefaultBlockRate = 100

	// DefaultPeriod specifies the default period at which profiles will be collected.
	DefaultPeriod = time.Minute

	// DefaultDuration specifies the default length of the CPU profile snapshot.
	DefaultDuration = time.Second * 15
)

const (
	defaultAPIURL      = "https://intake.profile.datadoghq.com/v1/input"
	defaultAgentHost   = "localhost"
	defaultAgentPort   = "8126"
	defaultEnv         = "none"
	defaultHTTPTimeout = 10 * time.Second // defines the current timeout before giving up with the send process
)

var defaultClient = &http.Client{
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
	Timeout: defaultHTTPTimeout,
}

var defaultProfileTypes = []ProfileType{MetricsProfile, CPUProfile, HeapProfile}

type config struct {
	apiKey string
	// targetURL is the upload destination URL. It will be set by the profiler on start to either apiURL or agentURL
	// based on the other options.
	targetURL     string
	apiURL        string // apiURL is the Datadog intake API URL
	agentURL      string // agentURL is the Datadog agent profiling URL
	service, env  string
	hostname      string
	statsd        StatsdClient
	httpClient    *http.Client
	tags          []string
	types         map[ProfileType]struct{}
	period        time.Duration
	cpuDuration   time.Duration
	mutexFraction int
	blockRate     int
}

func urlForSite(site string) (string, error) {
	u := fmt.Sprintf("https://intake.profile.%s/v1/input", site)
	_, err := url.Parse(u)
	return u, err
}

// isAPIKeyValid reports whether the given string is a structurally valid API key
func isAPIKeyValid(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, c := range key {
		if c > unicode.MaxASCII || (!unicode.IsLower(c) && !unicode.IsNumber(c)) {
			return false
		}
	}
	return true
}

func (c *config) addProfileType(t ProfileType) {
	if c.types == nil {
		c.types = make(map[ProfileType]struct{})
	}
	c.types[t] = struct{}{}
}

func defaultConfig() *config {
	c := config{
		env:           defaultEnv,
		apiURL:        defaultAPIURL,
		service:       filepath.Base(os.Args[0]),
		statsd:        &statsd.NoOpClient{},
		httpClient:    defaultClient,
		period:        DefaultPeriod,
		cpuDuration:   DefaultDuration,
		blockRate:     DefaultBlockRate,
		mutexFraction: DefaultMutexFraction,
		tags:          []string{fmt.Sprintf("pid:%d", os.Getpid())},
	}
	for _, t := range defaultProfileTypes {
		c.addProfileType(t)
	}

	agentHost, agentPort := defaultAgentHost, defaultAgentPort
	if v := os.Getenv("DD_AGENT_HOST"); v != "" {
		agentHost = v
	}
	if v := os.Getenv("DD_TRACE_AGENT_PORT"); v != "" {
		agentPort = v
	}
	WithAgentAddr(net.JoinHostPort(agentHost, agentPort))(&c)
	if v := os.Getenv("DD_API_KEY"); v != "" {
		WithAPIKey(v)(&c)
	}
	if v := os.Getenv("DD_SITE"); v != "" {
		WithSite(v)(&c)
	}
	if v := os.Getenv("DD_ENV"); v != "" {
		WithEnv(v)(&c)
	}
	if v := os.Getenv("DD_SERVICE"); v != "" {
		WithService(v)(&c)
	}
	if v := os.Getenv("DD_VERSION"); v != "" {
		WithVersion(v)(&c)
	}
	if v := os.Getenv("DD_TAGS"); v != "" {
		tags := strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' '
		})
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			WithTags(tag)(&c)
		}
	}
	WithTags(
		"profiler_version:"+version.Tag,
		"runtime_version:"+strings.TrimPrefix(runtime.Version(), "go"),
		"runtime_compiler:"+runtime.Compiler,
		"runtime_arch:"+runtime.GOARCH,
		"runtime_os:"+runtime.GOOS,
		"runtime-id:"+globalconfig.RuntimeID(),
	)(&c)
	// not for public use
	if v := os.Getenv("DD_PROFILING_URL"); v != "" {
		WithURL(v)(&c)
	}
	return &c
}

// An Option is used to configure the profiler's behaviour.
type Option func(*config)

// WithAgentAddr specifies the address to use when reaching the Datadog Agent.
func WithAgentAddr(hostport string) Option {
	return func(cfg *config) {
		cfg.agentURL = "http://" + hostport + "/profiling/v1/input"
	}
}

// WithAPIKey is deprecated and might be removed in future versions of this
// package. It allows to skip the agent and talk to the Datadog API directly
// using the provided API key.
func WithAPIKey(key string) Option {
	return func(cfg *config) {
		cfg.apiKey = key
	}
}

// WithURL specifies the HTTP URL for the Datadog Profiling API.
func WithURL(url string) Option {
	return func(cfg *config) {
		cfg.apiURL = url
	}
}

// WithPeriod specifies the interval at which to collect profiles.
func WithPeriod(d time.Duration) Option {
	return func(cfg *config) {
		cfg.period = d
	}
}

// CPUDuration specifies the length at which to collect CPU profiles.
func CPUDuration(d time.Duration) Option {
	return func(cfg *config) {
		cfg.cpuDuration = d
	}
}

// MutexProfileFraction turns on mutex profiles with rate indicating the fraction
// of mutex contention events reported in the mutex profile.
// On average, 1/rate events are reported.
// Setting an aggressive rate can hurt performance.
// For more information on this value, check runtime.SetMutexProfileFraction.
func MutexProfileFraction(rate int) Option {
	return func(cfg *config) {
		cfg.addProfileType(MutexProfile)
		cfg.mutexFraction = rate
	}
}

// BlockProfileRate turns on block profiles with the given rate.
// The profiler samples an average of one blocking event per rate nanoseconds spent blocked.
// For example, set rate to 1000000000 (aka int(time.Second.Nanoseconds())) to
// record one sample per second a goroutine is blocked.
// A rate of 1 catches every event.
// Setting an aggressive rate can hurt performance.
// For more information on this value, check runtime.SetBlockProfileRate.
func BlockProfileRate(rate int) Option {
	return func(cfg *config) {
		cfg.addProfileType(BlockProfile)
		cfg.blockRate = rate
	}
}

// WithProfileTypes specifies the profile types to be collected by the profiler.
func WithProfileTypes(types ...ProfileType) Option {
	return func(cfg *config) {
		// reset the types and only use what the user has specified
		for k := range cfg.types {
			delete(cfg.types, k)
		}
		cfg.addProfileType(MetricsProfile) // always report metrics
		for _, t := range types {
			cfg.addProfileType(t)
		}
	}
}

// WithService specifies the service name to attach to a profile.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.service = name
	}
}

// WithEnv specifies the environment to which these profiles should be registered.
func WithEnv(env string) Option {
	return func(cfg *config) {
		cfg.env = env
	}
}

// WithVersion specifies the service version tag to attach to profiles
func WithVersion(version string) Option {
	return WithTags("version:" + version)
}

// WithTags specifies a set of tags to be attached to the profiler. These may help
// filter the profiling view based on various information.
func WithTags(tags ...string) Option {
	return func(cfg *config) {
		cfg.tags = append(cfg.tags, tags...)
	}
}

// WithStatsd specifies an optional statsd client to use for metrics. By default,
// no metrics are sent.
func WithStatsd(client StatsdClient) Option {
	return func(cfg *config) {
		cfg.statsd = client
	}
}

// WithSite specifies the datadog site (datadoghq.com, datadoghq.eu, etc.)
// which profiles will be sent to.
func WithSite(site string) Option {
	return func(cfg *config) {
		u, err := urlForSite(site)
		if err != nil {
			log.Error("profiler: invalid site provided, using %s (%s)", defaultAPIURL, err)
			return
		}
		cfg.apiURL = u
	}
}

// WithHTTPClient specifies the HTTP client to use when submitting profiles to Site.
// In general, using this method is only necessary if you have need to customize the
// transport layer, for instance when using a unix domain socket.
func WithHTTPClient(client *http.Client) Option {
	return func(cfg *config) {
		cfg.httpClient = client
	}
}

// WithUDS configures the HTTP client to dial the Datadog Agent via the specified Unix Domain Socket path.
func WithUDS(socketPath string) Option {
	return WithHTTPClient(&http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
		Timeout: defaultHTTPTimeout,
	})
}
