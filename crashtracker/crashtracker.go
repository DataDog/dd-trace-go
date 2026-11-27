// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/crashtrackerstate"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
)

var (
	startOnce sync.Once
	startErr  error // preserved from the first Start call; returned on subsequent calls

	// superviseApplication is a variable so tests can simulate a supervision failure.
	superviseApplication = supervise
)

// Start starts crash tracking. It must be the first statement in main.
//
// See the package documentation for the full lifecycle, process model, and
// signal semantics.
//
// In the child, Start registers the inherited crash pipe and returns nil.
// It ignores options because the supervisor already resolved the configuration.
// If startup fails, Start returns an error and the application can continue
// without crash tracking. When disabled, Start returns nil without a child.
// There is no Stop. A deferred unregister would disable reports during a panic.
//
// Start is idempotent. Later calls return the result of the first call.
func Start(opts ...Option) error {
	startOnce.Do(func() { startErr = startAndRelease(opts...) })
	return startErr
}

// startAndRelease runs start, then lets the tracer and profiler start. The
// supervisor never returns from start. Every return is a process that continues
// as the application, so the tracer and profiler that init deferred start now.
func startAndRelease(opts ...Option) error {
	err := start(opts...)
	crashtrackerstate.Release()
	return err
}

func init() {
	crashtrackerstate.SetResolver(supervisorWillStart)
}

// supervisorWillStart reports whether Start, with the configuration that the
// Orchestrion integration passes, makes this process the supervisor. The
// Orchestrion integration passes no options, so the environment alone decides.
// A valid marker names the supervisor of this process, so this process is the
// application. Start removes the marker later, after the init functions.
func supervisorWillStart() bool {
	marker, _ := env.Lookup(supervisedFDEnv)
	if _, ok := parseSupervisedMarker(marker); ok {
		return false
	}
	return enabledFromEnvironment()
}

// enabledFromEnvironment resolves DD_CRASHTRACKING_ENABLED, which defaults to true.
func enabledFromEnvironment() bool {
	enabled, _, _ := stableconfig.Bool("DD_CRASHTRACKING_ENABLED", true)
	return enabled
}

func start(opts ...Option) error {
	marker, _ := env.Lookup(supervisedFDEnv)
	// A marker that names another supervisor is stray. Drop it and supervise.
	_ = os.Unsetenv(supervisedFDEnv)
	if fd, ok := parseSupervisedMarker(marker); ok {
		// The service-manager handoff must not depend on the pipe validation.
		if err := takeOverServiceManagerPIDs(); err != nil {
			return err
		}
		return registerCrashOutput(fd)
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	if !cfg.enabled {
		return nil
	}

	return superviseApplication(cfg)
}

// takeOverServiceManagerPIDs makes the application the owner of the systemd
// variables that name the supervisor. LISTEN_PID is the socket activation
// target. WATCHDOG_PID is the process that must send watchdog keep-alives.
// A value that names another process stays unchanged.
func takeOverServiceManagerPIDs() error {
	supervisor := strconv.Itoa(os.Getppid())
	for _, key := range []string{"LISTEN_PID", "WATCHDOG_PID"} {
		if pid, ok := os.LookupEnv(key); ok && pid == supervisor { //nolint:forbidigo // systemd process handoff, not tracer configuration.
			if err := os.Setenv(key, strconv.Itoa(os.Getpid())); err != nil {
				return fmt.Errorf("crashtracker: transfer %s: %w", key, err)
			}
		}
	}
	return nil
}

// parseSupervisedMarker returns the crash pipe descriptor in a marker of the
// form "<supervisor pid>:<fd>". It reports false unless the PID is the parent
// PID of this process.
func parseSupervisedMarker(marker string) (fd string, ok bool) {
	pid, fd, found := strings.Cut(marker, ":")
	if !found || pid != strconv.Itoa(os.Getppid()) {
		return "", false
	}
	return fd, true
}

// defaultConfig resolves attribution through the tracer's shared configuration,
// not the raw environment. It reads the same sources as the tracer: DD_*
// variables, declarative configuration, OTEL_SERVICE_NAME, and
// OTEL_RESOURCE_ATTRIBUTES (mapped to DD_TAGS). The supervisor never starts the
// tracer, so it cannot rely on values that the tracer stores when it starts.
func defaultConfig() *config {
	tracerConfig := internalconfig.Get()
	tags := stringTags(tracerConfig.GlobalTags())
	return &config{
		enabled: enabledFromEnvironment(),
		service: resolveService(tracerConfig.ServiceName(), tags),
		env:     resolveTag(tracerConfig.Env(), "env", tags),
		version: resolveTag(tracerConfig.Version(), "version", tags),
		site:    env.Get("DD_SITE"),
		apiKey:  env.Get("DD_API_KEY"),
	}
}

// stringTags keeps the string values of the tracer's global tags.
func stringTags(tags map[string]any) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// resolveTag resolves a single attribution value using the same precedence
// as the tracer's own DD_TAGS fallback (ddtrace/tracer/option.go): an
// explicit value (DD_ENV or DD_VERSION) wins outright; otherwise the
// same-named DD_TAGS entry, if any. Without this, service/env/version set only
// via DD_TAGS would resolve differently here than in the tracer, and a crash
// report would fail to attribute to the same service as that process's traces.
func resolveTag(explicit, tagKey string, tags map[string]string) string {
	if explicit != "" {
		return explicit
	}
	return tags[tagKey]
}

// resolveService resolves the service name when Start runs before the tracer
// does. globalconfig.ServiceName only returns a value the tracer previously
// stored via SetServiceName; it never reads DD_SERVICE itself, so a
// crashtracker.Start call in main before tracer.Start would otherwise drop
// the service tag entirely on every report.
//
// The final fallback matches tracer.Start's own default (ddtrace/tracer/option.go)
// rather than a literal "unknown": under the documented lifecycle, crashtracker.Start
// runs before tracer.Start sets that default, so without this the two would
// independently pick different values for the same process and crash reports
// would fail to correlate with that service's traces.
func resolveService(explicit string, tags map[string]string) string {
	if s := globalconfig.ServiceName(); s != "" {
		return s
	}
	if s := resolveTag(explicit, "service", tags); s != "" {
		return s
	}
	return filepath.Base(os.Args[0])
}
