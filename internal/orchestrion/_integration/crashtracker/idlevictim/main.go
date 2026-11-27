// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Command idlevictim is a real non-test main that runs for a short time and
// exits with code 0. It does not import crashtracker, the tracer, or the
// profiler. The Orchestrion aspects inject all three, so a test can count how
// many processes start the tracer and the profiler. It prints its own PID.
package main

import (
	"fmt"
	"os"
	"time"
)

func main() {
	// The test matches this PID against the process that the profiler reports.
	fmt.Fprintf(os.Stderr, "idlevictim: pid=%d\n", os.Getpid())
	time.Sleep(idleDuration)
}

// idleDuration gives the tracer and the profiler time to report to the fake
// agent before the application exits.
const idleDuration = 3 * time.Second
