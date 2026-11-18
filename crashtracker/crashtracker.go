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

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/stableconfig"
)

var (
	startOnce sync.Once
	startErr  error // preserved from the first Start call; returned on subsequent calls
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
	startOnce.Do(func() { startErr = start(opts...) })
	return startErr
}

func start(opts ...Option) error {
	marker, _ := env.Lookup(supervisedFDEnv)
	// A marker that names another supervisor is stray. Drop it and supervise.
	_ = os.Unsetenv(supervisedFDEnv)
	if fd, ok := parseSupervisedMarker(marker); ok {
		// The socket handoff must not depend on the pipe validation.
		if pid, ok := os.LookupEnv("LISTEN_PID"); ok && pid == strconv.Itoa(os.Getppid()) { //nolint:forbidigo // systemd process handoff, not tracer configuration.
			if err := os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid())); err != nil {
				return fmt.Errorf("crashtracker: transfer socket activation: %w", err)
			}
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

	return supervise(cfg)
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

func defaultConfig() *config {
	enabled, _, _ := stableconfig.Bool("DD_CRASHTRACKING_ENABLED", true)
	tags := internal.ParseTagString(env.Get("DD_TAGS"))
	return &config{
		enabled: enabled,
		service: resolveService(tags),
		env:     resolveTag("DD_ENV", "env", tags),
		version: resolveTag("DD_VERSION", "version", tags),
		site:    env.Get("DD_SITE"),
		apiKey:  env.Get("DD_API_KEY"),
	}
}

// resolveTag resolves a single attribution value using the same precedence
// as the tracer's own DD_TAGS fallback (ddtrace/tracer/option.go): an
// explicit envVar wins outright; otherwise the same-named DD_TAGS entry, if
// any. Without this, service/env/version set only via DD_TAGS (with
// DD_SERVICE/DD_ENV/DD_VERSION left unset) would resolve differently here
// than in the tracer, and a crash report would fail to attribute to the same
// service as that process's traces.
func resolveTag(envVar, tagKey string, tags map[string]string) string {
	if v := env.Get(envVar); v != "" {
		return v
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
func resolveService(tags map[string]string) string {
	if s := globalconfig.ServiceName(); s != "" {
		return s
	}
	if s := resolveTag("DD_SERVICE", "service", tags); s != "" {
		return s
	}
	return filepath.Base(os.Args[0])
}
