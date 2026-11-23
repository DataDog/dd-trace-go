// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Command victim is a real non-test main used to prove the crashtracker
// orchestrion aspect injects crashtracker.Start() as the first statement of
// main. It does not import crashtracker or call Start: a crash report is
// produced only if orchestrion injected the call.
package main

import (
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"
)

func main() {
	// The tracer and profiler aspects inject func init() calls, which the Go
	// runtime guarantees complete before main() runs, regardless of where
	// crashtracker's own prepended statement lands relative to any other
	// aspect's prepended statements inside main() itself. These two checks
	// observe that guarantee rather than assume it:
	//
	//   - globalconfig.ServiceName() is set unconditionally by tracer.Start()
	//     (ddtrace/tracer/option.go), even with zero DD_* configuration, so a
	//     non-empty value here can only mean the injected tracer.Start() (via
	//     its own func init()) already ran.
	//   - traceprof.SetProfilerEnabled sets an atomic flag and returns its
	//     previous value; profiler.Start() (run by its injected func init()
	//     when DD_PROFILING_ENABLED is set) sets that flag on success, so
	//     calling it again here with the same value is a non-destructive read
	//     of whether that already happened.
	tracerStarted := globalconfig.ServiceName() != ""
	profilerStarted := traceprof.SetProfilerEnabled(true)
	panic(fmt.Sprintf("orchestrion injection victim crash: tracer_started=%v profiler_started=%v", tracerStarted, profilerStarted))
}
