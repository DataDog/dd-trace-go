// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/osinfo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/traceprof"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler/internal/immutable"

	"github.com/DataDog/datadog-go/v5/statsd"
)

const (
	// DefaultMutexFraction specifies the mutex profile fraction to be used with the mutex profiler.
	// For more information or for changing this value, check MutexProfileFraction
	DefaultMutexFraction = 10

	// DefaultBlockRate specifies the default block profiling rate (in ns) used
	// by the block profiler. For more information or for changing this value,
	// check BlockProfileRate(). The default value of 100ms is somewhat
	// arbitrary. There is no provably safe value that will guarantee low
	// overhead for this profile type for all workloads. We don't recommend
	// enabling it under normal circumstances. See the link below for more
	// information: https://github.com/DataDog/go-profiler-notes/pull/15/files
	DefaultBlockRate = 100000000

	// DefaultPeriod specifies the default period at which profiles will be collected.
	DefaultPeriod = time.Minute

	// DefaultDuration specifies the default length of the CPU profile snapshot.
	DefaultDuration = time.Minute

	// DefaultUploadTimeout specifies the default timeout for uploading profiles.
	// It can be overwritten using the DD_PROFILING_UPLOAD_TIMEOUT env variable
	// or the WithUploadTimeout option.
	DefaultUploadTimeout = 10 * time.Second
)

const (
	defaultAPIURL    = "https://intake.profile.datadoghq.com/v1/input"
	defaultAgentHost = "localhost"
	defaultAgentPort = "8126"
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
}

var defaultProfileTypes = []ProfileType{MetricsProfile, CPUProfile, HeapProfile}

type config struct {
	apiKey    string
	agentless bool
	// targetURL is the upload destination URL. It will be set by the profiler on start to either apiURL or agentURL
	// based on the other options.
	targetURL            string
	apiURL               string // apiURL is the Datadog intake API URL
	agentURL             string // agentURL is the Datadog agent profiling URL
	service, env         string
	version              string
	hostname             string
	statsd               StatsdClient
	httpClient           *http.Client
	tags                 immutable.StringSlice
	customProfilerLabels []string
	types                map[ProfileType]struct{}
	period               time.Duration
	cpuDuration          time.Duration
	cpuProfileRate       int
	uploadTimeout        time.Duration
	maxGoroutinesWait    int
	mutexFraction        int
	blockRate            int
	outputDir            string
	deltaProfiles        bool
	logStartup           bool
	traceConfig          executionTraceConfig
	endpointCountEnabled bool
}

// logStartup records the configuration to the configured logger in JSON format
func logStartup(c *config) {
	var enabledProfiles []string
	for t := range c.types {
		enabledProfiles = append(enabledProfiles, t.String())
	}
	info := map[string]any{
		"date":                       time.Now().Format(time.RFC3339),
		"os_name":                    osinfo.OSName(),
		"os_version":                 osinfo.OSVersion(),
		"version":                    version.Tag,
		"lang":                       "Go",
		"lang_version":               runtime.Version(),
		"hostname":                   c.hostname,
		"delta_profiles":             c.deltaProfiles,
		"service":                    c.service,
		"env":                        c.env,
		"target_url":                 c.targetURL,
		"agentless":                  c.agentless,
		"tags":                       c.tags.Slice(),
		"profile_period":             c.period.String(),
		"enabled_profiles":           enabledProfiles,
		"cpu_duration":               c.cpuDuration.String(),
		"cpu_profile_rate":           c.cpuProfileRate,
		"block_profile_rate":         c.blockRate,
		"mutex_profile_fraction":     c.mutexFraction,
		"max_goroutines_wait":        c.maxGoroutinesWait,
		"upload_timeout":             c.uploadTimeout.String(),
		"execution_trace_enabled":    c.traceConfig.Enabled,
		"execution_trace_period":     c.traceConfig.Period.String(),
		"execution_trace_size_limit": c.traceConfig.Limit,
		"endpoint_count_enabled":     c.endpointCountEnabled,
		"custom_profiler_label_keys": c.customProfilerLabels,
	}
	b, err := json.Marshal(info)
	if err != nil {
		log.Error("Marshaling profiler configuration: %s", err)
		return
	}
	log.Info("Profiler configuration: %s\n", b)
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

func defaultConfig() (*config, error) {
	c := config{
		apiURL:               defaultAPIURL,
		service:              filepath.Base(os.Args[0]),
		statsd:               &statsd.NoOpClient{},
		httpClient:           defaultClient,
		period:               DefaultPeriod,
		cpuDuration:          DefaultDuration,
		blockRate:            DefaultBlockRate,
		mutexFraction:        DefaultMutexFraction,
		uploadTimeout:        DefaultUploadTimeout,
		maxGoroutinesWait:    1000, // arbitrary value, should limit STW to ~30ms
		deltaProfiles:        internal.BoolEnv("DD_PROFILING_DELTA", true),
		logStartup:           internal.BoolEnv("DD_TRACE_STARTUP_LOGS", true),
		endpointCountEnabled: internal.BoolEnv(traceprof.EndpointCountEnvVar, false),
	}
	c.tags = c.tags.Append(fmt.Sprintf("process_id:%d", os.Getpid()))
	for _, t := range defaultProfileTypes {
		c.addProfileType(t)
	}

	url := internal.AgentURLFromEnv()
	if url.Scheme == "unix" {
		WithUDS(url.Path)(&c)
	} else {
		c.agentURL = url.String() + "/profiling/v1/input"
	}
	if v := os.Getenv("DD_PROFILING_UPLOAD_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("DD_PROFILING_UPLOAD_TIMEOUT: %s", err)
		}
		WithUploadTimeout(d)(&c)
	}
	if v := os.Getenv("DD_API_KEY"); v != "" {
		WithAPIKey(v)(&c)
	}
	if internal.BoolEnv("DD_PROFILING_AGENTLESS", false) {
		WithAgentlessUpload()(&c)
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

	tags := make(map[string]string)
	if v := os.Getenv("DD_TAGS"); v != "" {
		tags = internal.ParseTagString(v)
		internal.CleanGitMetadataTags(tags)
	}
	for key, val := range internal.GetGitMetadataTags() {
		tags[key] = val
	}
	for key, val := range tags {
		if val != "" {
			WithTags(key + ":" + val)(&c)
		} else {
			WithTags(key)(&c)
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
	// not for public use
	if v := os.Getenv("DD_PROFILING_OUTPUT_DIR"); v != "" {
		withOutputDir(v)(&c)
	}
	if v := os.Getenv("DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("DD_PROFILING_WAIT_PROFILE_MAX_GOROUTINES: %s", err)
		}
		c.maxGoroutinesWait = n
	}

	// Experimental feature: Go execution trace (runtime/trace) recording.
	c.traceConfig.Refresh()
	return &c, nil
}

// An Option is used to configure the profiler's behaviour.
type Option func(*config)

// WithAgentAddr specifies the address to use when reaching the Datadog Agent.
func WithAgentAddr(hostport string) Option {
	return func(cfg *config) {
		cfg.agentURL = "http://" + hostport + "/profiling/v1/input"
	}
}

// WithAPIKey sets the Datadog API Key and takes precedence over the DD_API_KEY
// env variable. Historically this option was used to enable agentless
// uploading, but as of dd-trace-go v1.30.0 the behavior has changed to always
// default to agent based uploading which doesn't require an API key. So if you
// currently don't have an agent running on the default localhost:8126 hostport
// you need to set it up, or use WithAgentAddr to specify the hostport location
// of the agent. See WithAgentlessUpload for more information.
func WithAPIKey(key string) Option {
	return func(cfg *config) {
		cfg.apiKey = key
	}
}

// WithAgentlessUpload is currently for internal usage only and not officially
// supported. You should not enable it unless somebody at Datadog instructed
// you to do so. It allows to skip the agent and talk to the Datadog API
// directly using the provided API key.
func WithAgentlessUpload() Option {
	return func(cfg *config) {
		cfg.agentless = true
	}
}

// WithDeltaProfiles specifies if delta profiles are enabled. The default value
// is true. This option takes precedence over the DD_PROFILING_DELTA
// environment variable that can be set to "true" or "false" as well. See
// https://dtdg.co/go-delta-profile-docs for more information.
func WithDeltaProfiles(enabled bool) Option {
	return func(cfg *config) {
		cfg.deltaProfiles = enabled
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

// CPUProfileRate sets the sampling frequency for CPU profiling. A sample will
// be taken once for every (1 / hz) seconds of on-CPU time. If not given,
// profiling will use the default rate from the runtime/pprof.StartCPUProfile
// function, which is 100 as of Go 1.0.
//
// Setting a different profile rate will result in a spurious warning every time
// CPU profling is started, like "cannot set cpu profile rate until previous
// profile has finished". This is a known issue, but the rate will still be set
// correctly and CPU profiling will work.
func CPUProfileRate(hz int) Option {
	return func(cfg *config) {
		cfg.cpuProfileRate = hz
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

// BlockProfileRate turns on block profiles with the given rate. We do not
// recommend enabling this profile type, see DefaultBlockRate for more
// information. The rate is given in nanoseconds and a block event with a given
// duration has a min(duration/rate, 1) chance of getting sampled.
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
	return func(cfg *config) {
		cfg.version = version
	}
}

// WithTags specifies a set of tags to be attached to the profiler. These may help
// filter the profiling view based on various information.
func WithTags(tags ...string) Option {
	return func(cfg *config) {
		cfg.tags = cfg.tags.Append(tags...)
	}
}

// WithStatsd specifies an optional statsd client to use for metrics. By default,
// no metrics are sent.
func WithStatsd(client StatsdClient) Option {
	return func(cfg *config) {
		cfg.statsd = client
	}
}

// WithUploadTimeout specifies the timeout to use for uploading profiles. The
// default timeout is specified by DefaultUploadTimeout or the
// DD_PROFILING_UPLOAD_TIMEOUT env variable. Using a negative value or 0 will
// cause an error when starting the profiler.
func WithUploadTimeout(d time.Duration) Option {
	return func(cfg *config) {
		cfg.uploadTimeout = d
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
	return func(c *config) {
		// The HTTP client needs a valid URL. The host portion of the
		// url in particular can't just be the socket path, or else that
		// will be interpreted as part of the request path and the
		// request will fail.  Clean up the path here so we get
		// something resembling the desired path in any profiler logs.
		// TODO: copied from ddtrace/tracer, but is this correct?
		cleanPath := fmt.Sprintf("UDS_%s", strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(socketPath))
		c.agentURL = "http://" + cleanPath + "/profiling/v1/input"
		WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		})(c)
	}
}

// withOutputDir writes a copy of all uploaded profiles to the given
// directory. This is intended for local development or debugging uploading
// issues. The directory will keep growing, no cleanup is performed.
func withOutputDir(dir string) Option {
	return func(cfg *config) {
		cfg.outputDir = dir
	}
}

// WithLogStartup toggles logging the configuration of the profiler to standard
// error when profiling is started. The configuration is logged in a JSON
// format. This option is enabled by default.
func WithLogStartup(enabled bool) Option {
	return func(cfg *config) {
		cfg.logStartup = enabled
	}
}

// WithHostname sets the hostname which will be added to uploaded profiles
// through the "host:<hostname>" tag. If no hostname is given, the hostname will
// default to the output of os.Hostname()
func WithHostname(hostname string) Option {
	return func(cfg *config) {
		cfg.hostname = hostname
	}
}

// executionTraceConfig controls how often, and for how long, runtime execution
// traces are collected.
type executionTraceConfig struct {
	// Enabled indicates whether execution tracing is enabled.
	Enabled bool
	// Period is the amount of time between traces.
	Period time.Duration
	// Limit is the desired upper bound, in bytes, of a collected trace.
	// Traces may be slightly larger than this limit due to flushing pending
	// buffers at the end of tracing.
	//
	// We attempt to record for a full profiling period. The size limit of
	// the trace is a better proxy for overhead (it scales with the number
	// of events recorded) than duration, so we use that to decide when to
	// stop tracing.
	Limit int

	// warned is checked to prevent spamming a log every minute if the trace
	// config is invalid
	warned bool
}

// executionTraceEnabledDefault depends on the Go version and CPU architecture,
// see go_lt_1_21.go and this [article][] for more details.
//
// [article]: https://blog.felixge.de/waiting-for-go1-21-execution-tracing-with-less-than-one-percent-overhead/
var executionTraceEnabledDefault = runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64"

// Refresh updates the execution trace configuration to reflect any run-time
// changes to the configuration environment variables, applying defaults as
// needed.
func (e *executionTraceConfig) Refresh() {
	e.Enabled = internal.BoolEnv("DD_PROFILING_EXECUTION_TRACE_ENABLED", executionTraceEnabledDefault)
	e.Period = internal.DurationEnv("DD_PROFILING_EXECUTION_TRACE_PERIOD", 15*time.Minute)
	e.Limit = internal.IntEnv("DD_PROFILING_EXECUTION_TRACE_LIMIT_BYTES", defaultExecutionTraceSizeLimit)

	if e.Enabled && (e.Period == 0 || e.Limit == 0) {
		if !e.warned {
			e.warned = true
			log.Warn("Invalid execution trace config, enabled is true but size limit or frequency is 0. Disabling execution trace.")
		}
		e.Enabled = false
		return
	}
	// If the config is valid, reset e.warned so we'll print another warning
	// if it's udpated to be invalid
	e.warned = false
}

// WithCustomProfilerLabelKeys specifies [profiler label] keys which should be
// available as attributes for filtering frames for CPU and goroutine profile
// flame graphs in the Datadog profiler UI.
//
// The profiler is limited to 10 label keys to show in the UI. Any label keys
// after the first 10 will be ignored (but labels with ignored keys will still
// be available in the raw profile data).
//
// [profiler label]: https://rakyll.org/profiler-labels/
func WithCustomProfilerLabelKeys(keys ...string) Option {
	return func(cfg *config) {
		cfg.customProfilerLabels = append(cfg.customProfilerLabels, keys...)
	}
}
