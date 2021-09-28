// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mocktracer_test

import (
	"log"
	"testing"

	"gopkg.in/CodapeWild/dd-trace-go.v1/ddtrace/mocktracer"
)

func Example() {
	// Start the mock tracer.
	mt := mocktracer.Start()
	defer mt.Stop()

	// ...run some code with generates spans.

	// Query the mock tracer for finished spans.
	spans := mt.FinishedSpans()
	if len(spans) != 1 {
		// should only have 1 span
	}

	// Run assertions...
}

func TestSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	spans := mt.FinishedSpans()
	for _, span := range spans {
		log.Println(span)
	}
}
