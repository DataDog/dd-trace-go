// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"os"
	"path/filepath"
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
// The original process becomes a supervisor and does not return from Start.
// It runs the application in a child process with the same arguments and standard
// streams. All package initialization and any code before Start run twice.
// The application's PID differs from the container main PID. Its parent PID is
// the supervisor's PID. On Unix, the supervisor forwards signals to the child.
// It uploads any crash report before it exits with the child's exit status.
// The upload phase has an overall deadline of 10 seconds.
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
	if fd, ok := env.Lookup(supervisedFDEnv); ok {
		_ = os.Unsetenv(supervisedFDEnv)
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
