// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler_test

import (
	"log"

	"gopkg.in/DataDog/dd-trace-go.v1/profiler"
)

// This example illustrates how to run (and later stop) the Datadog Profiler.
func Example() {
	err := profiler.Start(
		profiler.WithAPIKey("123key"),
		profiler.WithEnv("staging"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer profiler.Stop()

	// ...
}
