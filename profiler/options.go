// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"net/http"
	"runtime"
	"time"

	v2 "github.com/DataDog/dd-trace-go/v2/profiler"
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

// An Option is used to configure the profiler's behaviour.
type Option = v2.Option

// WithAgentAddr specifies the address to use when reaching the Datadog Agent.
func WithAgentAddr(hostport string) Option {
	return v2.WithAgentAddr(hostport)
}

// WithAPIKey sets the Datadog API Key and takes precedence over the DD_API_KEY
// env variable. Historically this option was used to enable agentless
// uploading, but as of dd-trace-go v1.30.0 the behavior has changed to always
// default to agent based uploading which doesn't require an API key. So if you
// currently don't have an agent running on the default localhost:8126 hostport
// you need to set it up, or use WithAgentAddr to specify the hostport location
// of the agent. See WithAgentlessUpload for more information.
func WithAPIKey(key string) Option {
	return nil
}

// WithAgentlessUpload is currently for internal usage only and not officially
// supported. You should not enable it unless somebody at Datadog instructed
// you to do so. It allows to skip the agent and talk to the Datadog API
// directly using the provided API key.
func WithAgentlessUpload() Option {
	return nil
}

// WithDeltaProfiles specifies if delta profiles are enabled. The default value
// is true. This option takes precedence over the DD_PROFILING_DELTA
// environment variable that can be set to "true" or "false" as well. See
// https://dtdg.co/go-delta-profile-docs for more information.
func WithDeltaProfiles(enabled bool) Option {
	return v2.WithDeltaProfiles(enabled)
}

// WithURL specifies the HTTP URL for the Datadog Profiling API.
func WithURL(url string) Option {
	return v2.WithURL(url)
}

// WithPeriod specifies the interval at which to collect profiles.
func WithPeriod(d time.Duration) Option {
	return v2.WithPeriod(d)
}

// CPUDuration specifies the length at which to collect CPU profiles.
func CPUDuration(d time.Duration) Option {
	return v2.CPUDuration(d)
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
	return v2.CPUProfileRate(hz)
}

// MutexProfileFraction turns on mutex profiles with rate indicating the fraction
// of mutex contention events reported in the mutex profile.
// On average, 1/rate events are reported.
// Setting an aggressive rate can hurt performance.
// For more information on this value, check runtime.SetMutexProfileFraction.
func MutexProfileFraction(rate int) Option {
	return v2.MutexProfileFraction(rate)
}

// BlockProfileRate turns on block profiles with the given rate. We do not
// recommend enabling this profile type, see DefaultBlockRate for more
// information. The rate is given in nanoseconds and a block event with a given
// duration has a min(duration/rate, 1) chance of getting sampled.
func BlockProfileRate(rate int) Option {
	return v2.BlockProfileRate(rate)
}

// WithProfileTypes specifies the profile types to be collected by the profiler.
func WithProfileTypes(types ...ProfileType) Option {
	return v2.WithProfileTypes(types...)
}

// WithService specifies the service name to attach to a profile.
func WithService(name string) Option {
	return v2.WithService(name)
}

// WithEnv specifies the environment to which these profiles should be registered.
func WithEnv(env string) Option {
	return v2.WithEnv(env)
}

// WithVersion specifies the service version tag to attach to profiles
func WithVersion(version string) Option {
	return v2.WithVersion(version)
}

// WithTags specifies a set of tags to be attached to the profiler. These may help
// filter the profiling view based on various information.
func WithTags(tags ...string) Option {
	return v2.WithTags(tags...)
}

// WithStatsd specifies an optional statsd client to use for metrics. By default,
// no metrics are sent.
func WithStatsd(client StatsdClient) Option {
	return v2.WithStatsd(client)
}

// WithUploadTimeout specifies the timeout to use for uploading profiles. The
// default timeout is specified by DefaultUploadTimeout or the
// DD_PROFILING_UPLOAD_TIMEOUT env variable. Using a negative value or 0 will
// cause an error when starting the profiler.
func WithUploadTimeout(d time.Duration) Option {
	return v2.WithUploadTimeout(d)
}

// WithSite specifies the datadog site (datadoghq.com, datadoghq.eu, etc.)
// which profiles will be sent to.
func WithSite(site string) Option {
	return v2.WithSite(site)
}

// WithHTTPClient specifies the HTTP client to use when submitting profiles to Site.
// In general, using this method is only necessary if you have need to customize the
// transport layer, for instance when using a unix domain socket.
func WithHTTPClient(client *http.Client) Option {
	return v2.WithHTTPClient(client)
}

// WithUDS configures the HTTP client to dial the Datadog Agent via the specified Unix Domain Socket path.
func WithUDS(socketPath string) Option {
	return v2.WithUDS(socketPath)
}

// WithLogStartup toggles logging the configuration of the profiler to standard
// error when profiling is started. The configuration is logged in a JSON
// format. This option is enabled by default.
func WithLogStartup(enabled bool) Option {
	return v2.WithLogStartup(enabled)
}

// WithHostname sets the hostname which will be added to uploaded profiles
// through the "host:<hostname>" tag. If no hostname is given, the hostname will
// default to the output of os.Hostname()
func WithHostname(hostname string) Option {
	return v2.WithHostname(hostname)
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
	return v2.WithCustomProfilerLabelKeys(keys...)
}

// executionTraceEnabledDefault depends on the Go version and CPU architecture,
// see go_lt_1_21.go and this [article][] for more details.
//
// [article]: https://blog.felixge.de/waiting-for-go1-21-execution-tracing-with-less-than-one-percent-overhead/
var executionTraceEnabledDefault = runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64"
