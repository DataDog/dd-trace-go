// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/DataDog/datadog-go/statsd"
)

const (
	// DefaultMutexFraction specifies the mutex profile fraction to be used with the mutex profiler.
	// For more information or for changing this value, check runtime.SetMutexProfileFraction.
	DefaultMutexFraction = 10

	// DefaultBlockRate specifies the default block profiling rate used by the block profiler.
	// For more information or for changing this value, check runtime.SetBlockProfileRate.
	DefaultBlockRate = 100

	// DefaultPeriod specifies the default period at which profiles will be collected.
	DefaultPeriod = time.Minute

	// DefaultDuration specifies the default length of the CPU profile snapshot.
	DefaultDuration = time.Second * 15
)

const (
	defaultAPIURL    = "https://intake.profile.datadoghq.com/v1/input"
	defaultAgentHost = "localhost"
	defaultAgentPort = "8126"
	defaultEnv       = "none"
)

var defaultProfileTypes = []ProfileType{CPUProfile, HeapProfile}

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
		for _, tag := range strings.Split(v, ",") {
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
		"runtime-id:"+globalconfig.GetRuntimeID(),
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

// WithAPIKey specifies the API key to use when connecting to the Datadog API directly, skipping the agent.
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

// WithProfileTypes specifies the profile types to be collected by the profiler.
func WithProfileTypes(types ...ProfileType) Option {
	return func(cfg *config) {
		// reset the types and only use what the user has specified
		for k := range cfg.types {
			delete(cfg.types, k)
		}
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
